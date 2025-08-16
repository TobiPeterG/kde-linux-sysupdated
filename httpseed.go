// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2017, folbricht
// SPDX-FileCopyrightText: 2025 Harald Sitter <sitter@kde.org>

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/folbricht/desync"
)

type HTTPSeed struct {
	index    desync.Index
	pos      map[desync.ChunkID][]int
	location *url.URL

	mu sync.RWMutex
}

func NewHTTPSeed(location *url.URL, index desync.Index) *HTTPSeed {
	s := HTTPSeed{
		index:    index,
		pos:      make(map[desync.ChunkID][]int),
		location: location,
	}
	for i, c := range s.index.Chunks {
		s.pos[c.ID] = append(s.pos[c.ID], i)
	}
	return &s
}

// Returns a slice of chunks from the seed. Compares chunks from position 0
// with seed chunks starting at p. A "limit" value of zero means that there is no limit.
func (s *HTTPSeed) maxMatchFrom(chunks []desync.IndexChunk, p int, limit int) []desync.IndexChunk {
	if len(chunks) == 0 {
		return nil
	}
	var (
		sp int
		dp = p
	)
	for {
		if limit != 0 && sp == limit {
			break
		}
		if dp >= len(s.index.Chunks) || sp >= len(chunks) {
			break
		}
		if chunks[sp].ID != s.index.Chunks[dp].ID {
			break
		}
		dp++
		sp++
	}
	return s.index.Chunks[p:dp]
}

func (s *HTTPSeed) LongestMatchWith(chunks []desync.IndexChunk) (int, desync.SeedSegment) {
	s.mu.RLock()
	// isInvalid can be concurrently read or wrote. Use a mutex to avoid a race
	if len(chunks) == 0 || len(s.index.Chunks) == 0 {
		return 0, nil
	}
	s.mu.RUnlock()
	pos, ok := s.pos[chunks[0].ID]
	if !ok {
		return 0, nil
	}
	// From every position of chunks[0] in the source, find a slice of
	// matching chunks. Then return the longest of those slices.
	var (
		match []desync.IndexChunk
		max   int
		limit int
	)
	limit = 0
	desync.Log.Warnf("Longest lens %d -- %d", len(chunks), len(s.index.Chunks))
	for _, p := range pos {
		m := s.maxMatchFrom(chunks, p, limit)
		if len(m) > max {
			match = m
			max = len(m)
		}
		if limit != 0 && limit == max {
			break
		}
	}
	desync.Log.Warn("Longest match found for", chunks[0].ID, ":", max, "chunks")
	return max, newHTTPSeedSegment(s.location, match)
}

func (s *HTTPSeed) IsInvalid() bool {
	return false
}

func (s *HTTPSeed) SetInvalid(value bool) {
	panic("SetInvalid not implemented for HTTPSeed")
}

func (s *HTTPSeed) RegenerateIndex(ctx context.Context, n int, attempt int, seedNumber int) error {
	panic("RegenerateIndex not implemented for HTTPSeed")
}

type httpSeedSegment struct {
	chunks   []desync.IndexChunk
	location *url.URL
	isOpen   bool
	reader   io.ReadCloser
}

func newHTTPSeedSegment(location *url.URL, chunks []desync.IndexChunk) *httpSeedSegment {
	return &httpSeedSegment{
		chunks:   chunks,
		location: location,
		isOpen:   false,
	}
}

func (s *httpSeedSegment) FileName() string {
	return ""
}

func (s *httpSeedSegment) Size() uint64 {
	if len(s.chunks) == 0 {
		return 0
	}
	last := s.chunks[len(s.chunks)-1]
	return last.Start + last.Size - s.chunks[0].Start
}

func (s *httpSeedSegment) WriteInto(dst *os.File, offset, length, blocksize uint64, isBlank bool) (uint64, uint64, error) {
	panic("WriteInto not implemented for httpSeedSegment")
}

func (s *httpSeedSegment) Reader() (reader io.ReadCloser, err error) {
	segmentStart := s.chunks[0].Start
	segmentEnd := s.chunks[0].Start + s.Size() - 1

	request, err := http.NewRequest("GET", s.location.String(), nil)
	if err != nil {
		desync.Log.Errorf("Failed to create request for chunk %s: %v", s.chunks[0].ID, err)
		return nil, err
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", segmentStart, segmentEnd))

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}

	if response.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("unexpected status code %d", response.StatusCode)
	}

	return response.Body, nil
}

func (s *httpSeedSegment) Validate(file *os.File) error {
	panic("httpSeedSegment doesn't support validation")
}
