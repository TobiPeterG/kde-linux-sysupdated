// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2025-2026 Harald Sitter <sitter@kde.org>

package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
)

type Cache struct {
	path              string
	UpdateSize        uint64   `json:"update_size"`
	UpdateSizeContext []string `json:"update_size_context"`
}

func LoadCache(path string) Cache {
	cache := Cache{
		path: path,
	}

	err := os.Mkdir(filepath.Dir(path), 0755)
	if err != nil && !os.IsExist(err) {
		log.Println("Failed to create cache directory, cache will not be able to persist to disk: ", err)
		return cache
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Println("Cache doesn't exist yet, creating it: ", err)
		return cache
	}

	err = json.Unmarshal(data, &cache)
	if err != nil {
		log.Println("Failed to unmarshal cache, re-creating it: ", err)
		return cache
	}
	return cache
}

func (c *Cache) sync() {
	data, err := json.Marshal(c)
	if err != nil {
		log.Println("Failed to marshal cache, not saving to disk: ", err)
		return
	}

	err = os.WriteFile(c.path, data, 0600)
	if err != nil {
		log.Println("Failed to write cache to disk: ", err)
		return
	}
}

func (c *Cache) GetUpdateSize(context []string) (size uint64, err error) {
	if !slices.Equal(c.UpdateSizeContext, context) {
		return 0, os.ErrNotExist
	}

	return c.UpdateSize, nil
}

func (c *Cache) SetUpdateSize(size uint64, context []string) {
	c.UpdateSize = size
	c.UpdateSizeContext = context
}
