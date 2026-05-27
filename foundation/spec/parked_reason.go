// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// ParkReason categorizes why an executor parked a node. String-typed;
// the storage form on col:rimsky_node_runs.parked_reason is the
// lower_snake_case of the enum symbol (e.g.
// "PARK_REASON_AWAIT_CALLBACK" → "await_callback").
//
// Closed two-value set per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §ParkReason collapse. The proto wire layer
// (proto:executor.proto::ParkReason) caps the set at decode; the
// storage CHECK on col:rimsky_node_runs.parked_reason mirrors it.
//
// @concept: parked-state
type ParkReason string

const (
	// ParkReasonUnspecified is the empty-string sentinel used by the
	// supervisor to detect "no reason set yet" before the terminal
	// payload is read. NOT a valid stored value (storage CHECK
	// rejects).
	ParkReasonUnspecified ParkReason = ""
	// ParkReasonAwaitCallback: the run is parked waiting for an
	// external HTTP callback to resume it. No auto-resume.
	ParkReasonAwaitCallback ParkReason = "await_callback"
	// ParkReasonSnooze: the run is parked until resume_at elapses;
	// SweepParkedNodes wakes it on the deadline.
	ParkReasonSnooze ParkReason = "snooze"
)

// IsValid reports whether r is one of the closed-set values. The
// empty string (Unspecified) returns false; the supervisor
// populates it from the executor's terminal payload at park time.
func (r ParkReason) IsValid() bool {
	switch r {
	case ParkReasonAwaitCallback, ParkReasonSnooze:
		return true
	}
	return false
}

// String returns the lower_snake_case storage form.
func (r ParkReason) String() string { return string(r) }
