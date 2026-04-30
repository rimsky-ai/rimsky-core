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
// template's `locks: [...]` declarations enforced via rimsky_lock_holders.
//
// RequiredStores is denormalised from the template at enqueue time and
// drives the §6.2 supervisor-pool specialisation predicate. LastHeartbeatAt
// drives the §7.5 dispatch-claim sweep predicate (claim age tracks
// heartbeat liveness rather than initial-claim time).
type DispatchRow struct {
	ID              UUID
	NodeID          UUID
	ExecutorName    *string
	RequiredStores  []string
	EnqueuedAt      time.Time
	ClaimedBy       *string
	ClaimedAt       *time.Time
	LastHeartbeatAt *time.Time
	// FrameID is the frame this dispatch row belongs to (per
	// docs/specs/2026-04-26-frame-resolution-design.md §10.2). NOT NULL
	// in storage; blessed-invariant 19 forbids in-flight dispatch rows
	// without a frame_id.
	FrameID UUID
}

// RenderResourcePath renders segments as "a:b:c" for display.
func RenderResourcePath(segs []string) string {
	return strings.Join(segs, ":")
}
