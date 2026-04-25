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

// DispatchRow is the claimable unit of work (see spec §11.1).
type DispatchRow struct {
	ID              UUID
	NodeID          UUID
	ExecutorName    string
	ConcurrencyTags []string
	EnqueuedAt      time.Time
	ClaimedBy       *string
	ClaimedAt       *time.Time
}

// RenderResourcePath renders segments as "a:b:c" for display.
func RenderResourcePath(segs []string) string {
	return strings.Join(segs, ":")
}
