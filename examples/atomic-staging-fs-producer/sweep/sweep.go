// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package sweep reaps leaked staging directories — entries in the
// store's side-table whose claim_id is no longer live in rimsky's
// rimsky_claim_handles table AND whose CreatedAt is older than the
// configured TTL.
package sweep

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
)

// HandleSet is the abstraction over rimsky's live-claim-handle set.
// Production wiring queries rimsky_claim_handles via Postgres; the
// unit test fakes this with an in-memory set.
type HandleSet interface {
	Contains(claimID string) bool
}

// Sweeper drops leaked staging on a periodic tick.
type Sweeper struct {
	Store    *store.Store
	Live     HandleSet
	TTL      time.Duration
	Interval time.Duration
	Logger   func(format string, args ...any)
}

// Run blocks until ctx is cancelled, sweeping every Interval.
func (s *Sweeper) Run(ctx context.Context) error {
	if s.Interval <= 0 {
		s.Interval = 5 * time.Minute
	}
	if s.TTL <= 0 {
		s.TTL = 24 * time.Hour
	}
	if s.Logger == nil {
		s.Logger = func(string, ...any) {}
	}
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.Tick(time.Now()); err != nil {
				s.Logger("atomic-staging.sweep tick failed: %v", err)
			}
		}
	}
}

// Tick performs one sweep pass against the current clock.
// Public so the unit test can drive deterministic time.
func (s *Sweeper) Tick(now time.Time) error {
	entries, err := s.Store.Entries()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if s.Live.Contains(e.ClaimID) {
			continue // still in flight
		}
		if now.Sub(e.CreatedAt) < s.TTL {
			continue // young leak; give it a chance to land
		}
		if err := s.Store.AbandonByClaimID(e.ClaimID); err != nil {
			s.Logger("atomic-staging.sweep: abandon %s: %v", e.ClaimID, err)
		}
	}
	return nil
}
