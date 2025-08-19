// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2025 Harald Sitter <sitter@kde.org>

package main

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type IdleTimer struct {
	mu      sync.Mutex
	timer   *time.Timer
	waiters int
}

// Only resets the timer if this was the last waiter
func (it *IdleTimer) ReleaseAndMaybeReset() {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.waiters--
	if it.waiters == 0 {
		it.timer.Reset(idleTime)
	}
}

// Stops the timer and registers a waiter, you must defer a release to release the waiter when you are done
func (it *IdleTimer) StopAndClaim() {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.timer.Stop()
	it.waiters++
}

var idleTime = 5 * time.Minute
var idleTimer = &IdleTimer{
	mu:    sync.Mutex{},
	timer: time.NewTimer(idleTime),
}

func activityTracker() gin.HandlerFunc {
	return func(c *gin.Context) {
		idleTimer.StopAndClaim()
		defer idleTimer.ReleaseAndMaybeReset()

		c.Next()
	}
}
