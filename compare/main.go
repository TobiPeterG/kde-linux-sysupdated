// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2025 Harald Sitter <sitter@kde.org>

package main

import (
	"os"

	"github.com/folbricht/desync"
	"github.com/sirupsen/logrus"
)

func main() {
	desync.Log.SetOutput(os.Stdout)
	desync.Log.SetLevel(logrus.TraceLevel)

	store, err := desync.NewLocalIndexStore(".")
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ne, err := store.GetIndex(os.Args[2])
	if err != nil {
		desync.Log.Warn("Failed to get index:", err)
		return
	}

	oe, err := store.GetIndex(os.Args[1])
	if err != nil {
		desync.Log.Warn("Failed to get index:", err)
		return
	}

	new_chunks := make(map[desync.ChunkID]uint64)
	old_chunks := make(map[desync.ChunkID]uint64)

	for _, c := range ne.Chunks {
		new_chunks[c.ID] = c.Size
	}
	for _, c := range oe.Chunks {
		old_chunks[c.ID] = c.Size
	}

	missing_in_old := 0
	missing_in_old_size := uint64(0)
	for chunk, size := range new_chunks {
		if _, ok := old_chunks[chunk]; ok {
			continue
		}
		missing_in_old = missing_in_old + 1
		missing_in_old_size = missing_in_old_size + size
	}

	missing_in_new := 0
	missing_in_new_size := uint64(0)
	for chunk, size := range old_chunks {
		if _, ok := new_chunks[chunk]; ok {
			continue
		}
		missing_in_new = missing_in_new + 1
		missing_in_new_size = missing_in_new_size + size
	}

	desync.Log.Warnf("New index has %d chunks", len(ne.Chunks))
	desync.Log.Warnf("New index has %d chunks not in old index", missing_in_old)
	desync.Log.Warnf("New index has %d chunks not in old index, size %d MiB", missing_in_old, missing_in_old_size/1024/1024)

	desync.Log.Warnf("Old index has %d chunks", len(oe.Chunks))
	desync.Log.Warnf("Old index has %d chunks not in new index", missing_in_new)
	desync.Log.Warnf("Old index has %d chunks not in new index, size %d MiB", missing_in_new, missing_in_new_size/1024/1024)
}
