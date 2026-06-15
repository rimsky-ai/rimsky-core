// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// BreakpointCheckpoint enums for the breakpoint vocabulary. The SQL schema
// CHECK constraints carry the same string values (the schema can't
// reference Go constants); these typed constants are the canonical
// Go-side surface. Validators, runtime evaluators, and HTTP handlers
// reference these instead of bare string literals.
//
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

// BreakpointRow is the Go projection of rimsky_instance_breakpoints.
// Per concept:breakpoint (introduced by spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md).
//
// @concept: breakpoint
type BreakpointRow struct {
	ID             shared.UUID
	InstanceID     shared.UUID
	Matcher        map[string]any
	Checkpoint     BreakpointCheckpoint
	SignalType     *string // @constraint: nullable; only set for after_terminal checkpoint
	Mode           BreakpointMode
	OverflowPolicy BreakpointOverflowPolicy
	HitTTLSeconds  int
	TTLSeconds     *int // @constraint: nullable; instance-lifetime if null
	DroppedCount   int64
	CreatedByKey   string
	CreatedAt      time.Time
	ExpiresAt      *time.Time // @constraint: nullable; materialized from TTLSeconds at create
}

// BreakpointTable is the per-row-type accessor on rimsky_instance_breakpoints.
type BreakpointTable interface {
	Create(ctx context.Context, bp BreakpointRow, tx Tx) (shared.UUID, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointRow, error)
	ListForInstance(ctx context.Context, instanceID shared.UUID, includeExpired bool, tx Tx) ([]BreakpointRow, error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
	IncrementDropped(ctx context.Context, id shared.UUID, tx Tx) error
	SweepExpired(ctx context.Context, now time.Time, tx Tx) (int, error)
}

// BreakpointHitRow is the Go projection of rimsky_breakpoint_hits.
//
// @concept: breakpoint
type BreakpointHitRow struct {
	Seq           int64       // @constraint: monotonic cursor for resources/read pagination
	ID            shared.UUID // stable identity for the resume API
	BreakpointID  shared.UUID
	InstanceID    shared.UUID
	NodeRunID     *shared.UUID
	FrameID       *shared.UUID
	Checkpoint    BreakpointCheckpoint
	Mode          BreakpointMode
	Snapshot      map[string]any // @constraint: full payload per the 2026-05-24 instance-debugger spec §4.6
	HitAt         time.Time
	ResumedAt     *time.Time
	ResumedByKey  *string
	ResumeOverlay map[string]any // @constraint: nullable
}

// BreakpointHitTable is the per-row-type accessor on rimsky_breakpoint_hits.
type BreakpointHitTable interface {
	Create(ctx context.Context, hit BreakpointHitRow, tx Tx) (id shared.UUID, seq int64, err error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointHitRow, error)
	ListSinceForInstance(ctx context.Context, instanceID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
	ListSinceForBreakpoint(ctx context.Context, bpID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
	ListUnresumedForBreakpoint(ctx context.Context, bpID shared.UUID, tx Tx) ([]BreakpointHitRow, error)
	Resume(ctx context.Context, id shared.UUID, byKey string, overlay map[string]any, tx Tx) error
	AutoResumeStale(ctx context.Context, now time.Time, tx Tx) (int, error)
	DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx Tx) (int, error)
	UnresumedCount(ctx context.Context, bpID shared.UUID, tx Tx) (int, error)
	// @agent-contract: SweepOrphanedUnresumed deletes unresumed hits
	// whose hit_at is older than the cutoff AND whose parent
	// breakpoint's overflow_policy is NOT `auto_resume_after_ttl`
	// (those are handled by AutoResumeStale on their own per-breakpoint
	// hit_ttl). Reaps rows abandoned by supervisor restart mid-
	// block_dispatch poll — the supervisor never resumed them and no
	// other code path drains them, so without a reaper the queue
	// accumulates orphan rows over many restarts under load. The
	// cutoff should be generous enough that legitimately-blocked
	// dispatches under load don't get reaped out from under their
	// waiters. Returns the rowcount. Does NOT handle
	// `auto_resume_after_ttl` overflow — that path is AutoResumeStale.
	SweepOrphanedUnresumed(ctx context.Context, cutoff time.Time, tx Tx) (int, error)

	// @agent-contract: reports whether the instance has at least one
	// rimsky_breakpoint_hits row in pause mode with resumed_at IS NULL.
	// Drives the debug-channel gate (POST /instances/{id}/debug/override):
	// a pause-mode hit is the signal that the runner is suspended at a
	// breakpoint and the debugger surface may safely mutate node-runs /
	// attributes. Read inside the request tx so the gate-check shares the
	// snapshot with the mutation step (TOCTOU resistance per the Pass 9
	// falsifier).
	// @concept: breakpoint
	HasUnresumedPauseHitForInstance(ctx context.Context, instanceID shared.UUID, tx Tx) (bool, error)
}
