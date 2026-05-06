// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package shared

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

type UUID = uuid.UUID

// NodeState: fresh | stale | running | failed
type NodeState string

const (
	NodeStateFresh   NodeState = "fresh"
	NodeStateStale   NodeState = "stale"
	NodeStateRunning NodeState = "running"
	NodeStateFailed  NodeState = "failed"
)

// LastOutcome is the resolution flavor recorded on rimsky_nodes for
// terminal-for-this-frame transitions. Distinct from NodeState; the
// node's state machine is unchanged. last_outcome lives on the
// rimsky_nodes row alongside state and is written by the same
// transition that lands the node in fresh or failed.
//
// Values are persisted as TEXT under both Postgres and SQLite. NULL
// means "no outcome recorded yet" (legacy fresh nodes pre-migration).
//
// See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.2.
type LastOutcome string

const (
	LastOutcomeFreshChanged   LastOutcome = "fresh_changed"
	LastOutcomeFreshUnchanged LastOutcome = "fresh_unchanged"
	LastOutcomePassed         LastOutcome = "passed"
	LastOutcomePureCascade    LastOutcome = "pure_cascade"
	LastOutcomeFailed         LastOutcome = "failed"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type BackoffKind string

const (
	BackoffLinear      BackoffKind = "linear"
	BackoffExponential BackoffKind = "exponential"
)

type JitterKind string

const (
	JitterNone      JitterKind = "none"
	JitterPlusMinus JitterKind = "plus_minus"
)

type AccessKind string

const (
	AccessInline AccessKind = "inline"
	AccessSQL    AccessKind = "sql"
	AccessMCP    AccessKind = "mcp"
	AccessREST   AccessKind = "rest"
)

type MessageType string

const (
	MessageInvalidate  MessageType = "invalidate"
	MessageRecalculate MessageType = "recalculate"
)

// DispatchRow is the claimable unit of work (see spec §9.6 / §11.1).
//
// ExecutorName is nullable: native (claim-only) nodes have no executor and
// are run by the supervisor's omnibus runner directly (spec §7.3). The
// concurrency_tags field is gone — per-node concurrency now lives in the
// template's `locks: [...]` declarations enforced via rimsky_claim_handle.
//
// RequiredStores is denormalised from the template at enqueue time and
// drives the §6.2 supervisor-pool specialisation predicate. LastHeartbeatAt
// drives the §7.5 dispatch-claim sweep predicate (claim age tracks
// heartbeat liveness rather than initial-claim time).
type DispatchRow struct {
	ID              UUID       `json:"id"`
	NodeID          UUID       `json:"node_id"`
	ExecutorName    *string    `json:"executor_name,omitempty"`
	RequiredStores  []string   `json:"required_stores,omitempty"`
	EnqueuedAt      time.Time  `json:"enqueued_at"`
	ClaimedBy       *string    `json:"claimed_by,omitempty"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	// FrameID is the frame this dispatch row belongs to (per
	// docs/history/2026-04-26-frame-resolution-design.md §10.2). NOT NULL
	// in storage; blessed-invariant 19 forbids in-flight dispatch rows
	// without a frame_id.
	FrameID UUID `json:"frame_id"`
}

// RenderResourcePath renders segments as "a:b:c" for display.
func RenderResourcePath(segs []string) string {
	return strings.Join(segs, ":")
}
