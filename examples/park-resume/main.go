// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

type rateLimitOnce struct {
	mu         sync.Mutex
	attempts   map[string]int
	retryAfter int
}

func newRateLimitOnce(retryAfterSeconds int) *rateLimitOnce {
	return &rateLimitOnce{attempts: make(map[string]int), retryAfter: retryAfterSeconds}
}

// @decision: bundled-recipes-production-paths
func (h *rateLimitOnce) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.attempts[r.URL.Path]++
	attempt := h.attempts[r.URL.Path]
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if attempt == 1 {
		w.Header().Set("Retry-After", strconv.Itoa(h.retryAfter))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "rate_limited",
			"attempt": attempt,
		})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"report":  "ready",
		"attempt": attempt,
	})
}

func main() {
	retryAfter := 2
	if v := os.Getenv("RATELIMIT_RETRY_AFTER_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			log.Fatalf("RATELIMIT_RETRY_AFTER_SECONDS must be a non-negative integer, got %q", v)
		}
		retryAfter = n
	}
	addr := "0.0.0.0:9420"
	log.Printf("park-ratelimit: listening on %s (Retry-After: %ds; first request per path is 429, retries succeed)", addr, retryAfter)
	if err := http.ListenAndServe(addr, newRateLimitOnce(retryAfter)); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
