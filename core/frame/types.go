package frame

import (
	"time"

	"github.com/google/uuid"
)

// Mode discriminates how a stream of invalidate events is collapsed into
// frames. See spec §3 for the per-template choice.
type Mode string

const (
	// ModeCoalesce collapses successive invalidates while a frame is
	// running into a single trailing frame whose source set unions the
	// pending sources.
	ModeCoalesce Mode = "coalesce"
	// ModeSerialQueue creates one frame per invalidate; frames execute
	// strictly in arrival order per instance.
	ModeSerialQueue Mode = "serial_queue"
)

// State is the lifecycle of a rimsky_frames row.
type State string

const (
	// StateQueued means the frame is waiting for an empty running slot
	// (or for additional sources to be coalesced into it).
	StateQueued State = "queued"
	// StateRunning means the frame is the unique active frame for its
	// instance. Sources have transitioned to stale; cascade is in flight.
	StateRunning State = "running"
	// StateCompleted means the frame's cascade resolved without any
	// failed nodes.
	StateCompleted State = "completed"
	// StateFailed means at least one node in the frame's cascade ended
	// in state=failed.
	StateFailed State = "failed"
)

// Frame mirrors the rimsky_frames row shape.
//
// SourceNodeIDs is a non-empty array of node IDs that originated the frame
// (one for serial_queue arrival; potentially many for a coalesced frame).
type Frame struct {
	ID             uuid.UUID
	InstanceID     uuid.UUID
	Mode           Mode
	State          State
	SourceNodeIDs  []uuid.UUID
	QueuedAt       time.Time
	StartedAt      *time.Time
	EndedAt        *time.Time
	FrameTimeoutMs int64
}
