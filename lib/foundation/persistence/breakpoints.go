// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: breakpoint
type BreakpointCheckpoint string

const (
	CheckpointBeforeDispatch BreakpointCheckpoint = "before_dispatch"
	CheckpointAfterTerminal  BreakpointCheckpoint = "after_terminal"
)

type BreakpointMode string

const (
	BreakpointModePause      BreakpointMode = "pause"
	BreakpointModeNotifyOnly BreakpointMode = "notify_only"
)

type BreakpointOverflowPolicy string

const (
	OverflowDropOldest         BreakpointOverflowPolicy = "drop_oldest"
	OverflowBlockDispatch      BreakpointOverflowPolicy = "block_dispatch"
	OverflowAutoResumeAfterTTL BreakpointOverflowPolicy = "auto_resume_after_ttl"
)

// @concept: breakpoint
type BreakpointRow struct {
	ID             shared.UUID
	InstanceID     shared.UUID
	Matcher        map[string]any
	Checkpoint     BreakpointCheckpoint
	SignalType     *string
	Mode           BreakpointMode
	OverflowPolicy BreakpointOverflowPolicy
	HitTTLSeconds  int
	TTLSeconds     *int
	DroppedCount   int64
	CreatedByKey   string
	CreatedAt      time.Time
	ExpiresAt      *time.Time
}

type BreakpointTable interface {
	Create(ctx context.Context, bp BreakpointRow, tx Tx) (shared.UUID, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointRow, error)
	ListForInstance(ctx context.Context, instanceID shared.UUID, includeExpired bool, tx Tx) ([]BreakpointRow, error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
	IncrementDropped(ctx context.Context, id shared.UUID, tx Tx) error
	SweepExpired(ctx context.Context, now time.Time, tx Tx) (int, error)

	Lock(ctx context.Context, id shared.UUID, tx Tx) error
}

// @concept: breakpoint
type BreakpointHitRow struct {
	Seq           int64
	ID            shared.UUID
	BreakpointID  shared.UUID
	InstanceID    shared.UUID
	NodeRunID     *shared.UUID
	FrameID       *shared.UUID
	Checkpoint    BreakpointCheckpoint
	Mode          BreakpointMode
	Snapshot      map[string]any
	HitAt         time.Time
	ResumedAt     *time.Time
	ResumedByKey  *string
	ResumeOverlay map[string]any
}

type BreakpointHitTable interface {
	Create(ctx context.Context, hit BreakpointHitRow, tx Tx) (id shared.UUID, seq int64, err error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointHitRow, error)
	ListSinceForInstance(ctx context.Context, instanceID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
	ListSinceForBreakpoint(ctx context.Context, bpID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
	Resume(ctx context.Context, id shared.UUID, byKey string, overlay map[string]any, tx Tx) error
	AutoResumeStale(ctx context.Context, now time.Time, tx Tx) (int, error)
	DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx Tx) (int, error)
	UnresumedCount(ctx context.Context, bpID shared.UUID, tx Tx) (int, error)
	SweepOrphanedUnresumed(ctx context.Context, cutoff time.Time, tx Tx) (int, error)

	// @concept: breakpoint
	HasUnresumedPauseHitForInstance(ctx context.Context, instanceID shared.UUID, tx Tx) (bool, error)
}
