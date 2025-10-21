// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2017, folbricht
// SPDX-FileCopyrightText: 2025 Harald Sitter <sitter@kde.org>

package main

import (
	"context"
	"os"

	"github.com/folbricht/desync"
	"golang.org/x/sync/errgroup"
)

// IndexSegment represents a contiguous section of an index which is used when
// assembling a file from seeds. first/last are positions in the index.
type IndexSegment struct {
	index       desync.Index
	first, last int
}

func (s IndexSegment) lengthChunks() int {
	return s.last - s.first + 1
}

func (s IndexSegment) lengthBytes() uint64 {
	return s.end() - s.start()
}

func (s IndexSegment) start() uint64 {
	return s.index.Chunks[s.first].Start
}

func (s IndexSegment) end() uint64 {
	return s.index.Chunks[s.last].Start + s.index.Chunks[s.last].Size
}

func (s IndexSegment) chunks() []desync.IndexChunk {
	return s.index.Chunks[s.first : s.last+1]
}

// SeedSequencer is used to find sequences of chunks from seed files when assembling
// a file from an index. Using seeds reduces the need to download and decompress chunks
// from chunk stores. It also enables the use of reflinking/cloning of sections of
// files from a seed file where supported to reduce disk usage.
type SeedSequencer struct {
	storeSeed desync.Seed
	seeds     []desync.Seed
	index     desync.Index
	current   int
}

// SeedSegmentCandidate represent a single segment that we expect to use
// in a Plan
type SeedSegmentCandidate struct {
	seed         desync.Seed
	source       desync.SeedSegment
	indexSegment IndexSegment
}

type Plan []SeedSegmentCandidate

var MockValidate = false

// NewSeedSequencer initializes a new sequencer from a number of seeds.
func NewSeedSequencer(idx desync.Index, storeSeed desync.Seed, src ...desync.Seed) *SeedSequencer {
	return &SeedSequencer{
		storeSeed: storeSeed,
		seeds:     src,
		index:     idx,
	}
}

// Plan returns a new possible plan, representing an ordered list of
// segments that can be used to re-assemble the requested file
func (r *SeedSequencer) Plan() (plan Plan) {
	for {
		seed, segment, source, done := r.Next()
		plan = append(plan, SeedSegmentCandidate{seed, source, segment})
		if done {
			break
		}
	}

	{ // debugging
		fromLocal := 0
		fromLocalSize := uint64(0)
		fromRemote := 0
		fromRemoteSize := uint64(0)

		for _, s := range plan {
			if s.source.FileName() != "" { // local seeds always have a filename
				fromLocal += 1
				fromLocalSize += s.indexSegment.lengthBytes()
			} else {
				fromRemote += 1
				fromRemoteSize += s.indexSegment.lengthBytes()
			}
		}

		desync.Log.Warnf("Plan with holes: %d segments for %d chunks", len(plan), len(r.index.Chunks))
		desync.Log.Warnf("Local %d MiB; %d segments | Remote %d MiB; %d segments", fromLocalSize/1024/1024, fromLocal, fromRemoteSize/1024/1024, fromRemote)
	}

	return plan
}

// Next returns a sequence of index chunks (from the target index) and the
// longest matching segment from one of the seeds. If source is nil, no
// match was found in the seeds and the chunk needs to be retrieved from a
// store. If done is true, the sequencer is complete.
func (r *SeedSequencer) Next() (seed desync.Seed, segment IndexSegment, source desync.SeedSegment, done bool) {
	var (
		max     uint64
		advance = 1
	)
	for _, s := range r.seeds {
		n, m := s.LongestMatchWith(r.index.Chunks[r.current:])
		if n > 0 && m.Size() > max {
			seed = s
			source = m
			advance = n
			max = m.Size()
		}
	}

	if source == nil {
		// found no match at all. look for the first possible match in our storeSeed.
		// The expectation here is that we will find a match in the storeSeed.
		// We'll want to have the largest contiguous segment between here and the next viable seed segment.
		nextSegmentAt := -1
		for index, _ := range r.index.Chunks[r.current:] {
			chunkIndex := r.current + index
			for _, s := range r.seeds {
				_, source := s.LongestMatchWith(r.index.Chunks[chunkIndex:])
				if source != nil {
					nextSegmentAt = chunkIndex
					break
				}
			}
			if nextSegmentAt != -1 {
				break
			}
		}

		if nextSegmentAt == -1 {
			// Load the rest out of the storeSeed
			nextSegmentAt = len(r.index.Chunks)
		}

		// Get the longest match between current and nextSegmentAt
		n, m := r.storeSeed.LongestMatchWith(r.index.Chunks[r.current:nextSegmentAt])
		if n > 0 && m.Size() > max {
			seed = r.storeSeed
			source = m
			advance = n
			max = m.Size()
		}
	}

	segment = IndexSegment{index: r.index, first: r.current, last: r.current + advance - 1}
	r.current += advance
	return seed, segment, source, r.current >= len(r.index.Chunks)
}

// Rewind resets the current target index to the beginning.
func (r *SeedSequencer) Rewind() {
	r.current = 0
}

// isFileSeed returns true if this segment is pointing to a fileSeed
func (s SeedSegmentCandidate) isFileSeed() bool {
	// We expect an empty filename when using nullSeeds
	return s.source != nil && s.source.FileName() != ""
}

// RegenerateInvalidSeeds regenerates the index to match the unexpected seed content
func (r *SeedSequencer) RegenerateInvalidSeeds(ctx context.Context, n int, attempt int) error {
	seedNumber := 1
	for _, s := range r.seeds {
		if s.IsInvalid() {
			if err := s.RegenerateIndex(ctx, n, attempt, seedNumber); err != nil {
				return err
			}
			seedNumber += 1
		}
	}
	return nil
}

// Validate validates a proposed plan by checking if all the chosen chunks
// are correctly provided from the seeds. In case a seed has invalid chunks, the
// entire seed is marked as invalid and an error is returned.
func (p Plan) Validate(ctx context.Context, n int, pb desync.ProgressBar) (err error) {
	type Job struct {
		candidate SeedSegmentCandidate
		file      *os.File
	}
	var (
		in      = make(chan Job)
		fileMap = make(map[string]*os.File)
	)
	if MockValidate {
		// This is used in the automated tests to mock a plan that is valid
		return nil
	}
	length := 0
	for _, s := range p {
		if !s.isFileSeed() {
			continue
		}
		length += s.indexSegment.lengthChunks()
	}
	pb.SetTotal(length)
	pb.Start()
	defer pb.Finish()
	// Share a single file descriptor per seed for all the goroutines
	for _, s := range p {
		if !s.isFileSeed() {
			continue
		}
		name := s.source.FileName()
		if _, present := fileMap[name]; present {
			continue
		} else {
			file, err := os.Open(name)
			if err != nil {
				// We were not able to open the seed. Mark it as invalid and return
				s.seed.SetInvalid(true)
				return err
			}
			fileMap[name] = file
			defer file.Close()
		}
	}
	g, ctx := errgroup.WithContext(ctx)
	// Concurrently validate all the chunks in this plan
	for i := 0; i < n; i++ {
		g.Go(func() error {
			for job := range in {
				if err := job.candidate.source.Validate(job.file); err != nil {
					job.candidate.seed.SetInvalid(true)
					return err
				}
				pb.Add(job.candidate.indexSegment.lengthChunks())
			}
			return nil
		})
	}

loop:
	for _, s := range p {
		if !s.isFileSeed() {
			// This is not a fileSeed, we have nothing to validate
			continue
		}
		select {
		case <-ctx.Done():
			break loop
		case in <- Job{s, fileMap[s.source.FileName()]}:
		}
	}
	close(in)

	return g.Wait()
}
