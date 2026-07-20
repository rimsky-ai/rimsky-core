// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package sweep

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
)

type HandleSet interface {
	Contains(claimID string) bool
}

type Sweeper struct {
	Store    *store.Store
	Live     HandleSet
	TTL      time.Duration
	Interval time.Duration
	Logger   func(format string, args ...any)
}

func (s *Sweeper) Run(ctx context.Context) error {
	if s.Interval <= 0 {
		s.Interval = 5 * time.Minute
	}
	if s.TTL <= 0 {
		s.TTL = 24 * time.Hour
	}
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.Tick(time.Now()); err != nil {
				s.logf("atomic-staging.sweep tick failed: %v", err)
			}
		}
	}
}

func (s *Sweeper) logf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger(format, args...)
	}
}

func (s *Sweeper) Tick(now time.Time) error {
	entries, err := s.Store.Entries()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if s.Live.Contains(e.ClaimID) {
			continue
		}
		if now.Sub(e.CreatedAt) < s.TTL {
			continue
		}
		if err := s.Store.AbandonByClaimID(e.ClaimID); err != nil {
			s.logf("atomic-staging.sweep: abandon %s: %v", e.ClaimID, err)
		}
	}
	return nil
}
