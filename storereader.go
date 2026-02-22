// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2017, folbricht
// SPDX-FileCopyrightText: 2026 Harald Sitter <sitter@kde.org>

package main

import (
	"io"

	"github.com/folbricht/desync"
)

type ChunkReader struct {
	store      desync.Store
	indexChunk desync.IndexChunk
	chunk      *desync.Chunk
	offset     int
}

func (cr *ChunkReader) Close() error {
	return nil
}

func (cr *ChunkReader) Read(p []byte) (n int, err error) {
	if cr.chunk == nil { // only open retrieve the chunk once; Read() may run multiple times
		cr.chunk, err = cr.store.GetChunk(cr.indexChunk.ID)
		if err != nil {
			desync.Log.Error("Failed to get chunk from store:", err)
			return n, err
		}
	}

	data, err := cr.chunk.Data() // this caches internally
	if err != nil {
		desync.Log.Error("Failed to read chunk data:", err)
		return n, err
	}

	if cr.offset >= len(data) {
		return 0, io.EOF
	}
	n = copy(p, data[cr.offset:])
	cr.offset = cr.offset + n

	err = nil
	if cr.offset >= len(data) {
		err = io.EOF
	}

	return n, err
}
