// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2017, folbricht
// SPDX-FileCopyrightText: 2025 Harald Sitter <sitter@kde.org>

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/folbricht/desync"
	"github.com/spatialcurrent/go-lazy/pkg/lazy"
)

// InvalidSeedAction represent the action that we will take if a seed
// happens to be invalid. There are currently three options:
// - fail with an error
// - skip the invalid seed and try to continue
// - regenerate the invalid seed index
type InvalidSeedAction int

const (
	InvalidSeedActionBailOut InvalidSeedAction = iota
	InvalidSeedActionSkip
	InvalidSeedActionRegenerate
)

type AssembleOptions struct {
	InvalidSeedAction InvalidSeedAction
}

type Assembler struct {
	plan  Plan
	store desync.Store
}

type ReaderReader interface {
	Reader() (io.ReadCloser, error)
}

func (a *Assembler) Readers() (readers []io.ReadCloser) {
	for _, segment := range a.plan {
		if segment.source == nil {
			desync.Log.Error("Segment source is nil", segment)
			panic("Segment source is nil")
		}

		if a.store != nil && segment.source.FileName() == "" { // if we have a store prefer that over the http seed
			desync.Log.Debug("Getting chunk from store for segment ", len(segment.indexSegment.chunks()), " chunks")
			for _, chunk := range segment.indexSegment.chunks() {
				reader := &ChunkReader{
					store:      a.store,
					indexChunk: chunk,
				}
				readers = append(readers, reader)
			}
			continue
		}

		if readerSegment, ok := segment.source.(ReaderReader); ok {
			reader := lazy.NewLazyReader(func() (reader io.Reader, err error) {
				reader, err = readerSegment.Reader()
				if err != nil {
					desync.Log.Error("Failed to get reader from segment source", segment.source, ":", err)
					return nil, err
				}
				return reader, nil
			})
			readers = append(readers, reader)
			continue
		}

		desync.Log.Error("Segment source is not a ReaderReader", segment.source)
		panic("Segment source is not a ReaderReader")
	}

	if len(readers) == 0 {
		desync.Log.Warn("No readers created for assembler plan")
	}
	desync.Log.Debugf("Created %d readers for assembler plan", len(readers))
	return readers
}

func stream(ctx context.Context, idx desync.Index, storeSeed desync.Seed, store desync.Store, seeds []desync.Seed, options AssembleOptions) (*Assembler, error) {
	type Job struct {
		segment IndexSegment
		source  desync.SeedSegment
	}
	var (
		attempt = 1
	)

	// Let the sequencer break up the index into segments, create and validate a plan,
	// feed the workers, and stop if there are any errors
	seq := NewSeedSequencer(idx, storeSeed, seeds...)
	plan := seq.Plan()
	for {
		validatingPrefix := fmt.Sprintf("Attempt %d: Validating ", attempt)
		if err := plan.Validate(ctx, 1, desync.NewProgressBar(validatingPrefix)); err != nil {
			// This plan has at least one invalid seed
			switch options.InvalidSeedAction {
			case InvalidSeedActionBailOut:
				return nil, err
			case InvalidSeedActionRegenerate:
				desync.Log.WithError(err).Info("Unable to use one of the chosen seeds, regenerating it")
				if err := seq.RegenerateInvalidSeeds(ctx, 1, attempt); err != nil {
					return nil, err
				}
			case InvalidSeedActionSkip:
				// Recreate the plan. This time the seed marked as invalid will be skipped
				desync.Log.WithError(err).Info("Unable to use one of the chosen seeds, skipping it")
			default:
				panic("Unhandled InvalidSeedAction")
			}

			attempt += 1
			seq.Rewind()
			plan = seq.Plan()
			continue
		}
		// Found a valid plan
		break
	}

	return &Assembler{
		plan:  plan,
		store: store,
	}, nil

}
