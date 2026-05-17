// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// ParkReason categorizes why an executor parked a node. String-typed;
// the storage form on col:rimsky_node_runs.parked_reason is the
// lower_snake_case of the enum symbol (e.g.
// "PARK_REASON_CALLBACK_WAIT" → "callback_wait").
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Parked-state taxonomy. The 4-reason taxonomy
// (TIME_WAIT / CALLBACK_WAIT / RETRY_BACKOFF / OTHER) is the canonical
// set for new emitters; the additional values
// (SIGNAL_WAIT / AWAITING_HUMAN) are retained for existing emit sites
// and map to OTHER in the rolled-up per-reason max_park_duration
// caps when not explicitly configured.
//
// OTHER requires the sibling reason_label column non-empty;
// validation enforced at the supervisor's terminal handler.
//
// @concept: parked-state
type ParkReason string

const (
	ParkReasonUnspecified  ParkReason = ""
	ParkReasonTimeWait     ParkReason = "time_wait"
	ParkReasonCallbackWait ParkReason = "callback_wait"
	ParkReasonRetryBackoff ParkReason = "retry_backoff"
	ParkReasonOther        ParkReason = "other"
	// Legacy / pre-spec values; mapped to OTHER for caps.
	ParkReasonSignalWait    ParkReason = "signal_wait"
	ParkReasonAwaitingHuman ParkReason = "awaiting_human"
)

// IsValid reports whether r is one of the recognized constants
// (including the legacy values). The empty string (Unspecified)
// returns false; the supervisor populates it from the executor's
// terminal payload at park time.
func (r ParkReason) IsValid() bool {
	switch r {
	case ParkReasonTimeWait, ParkReasonCallbackWait, ParkReasonRetryBackoff,
		ParkReasonOther, ParkReasonSignalWait, ParkReasonAwaitingHuman:
		return true
	}
	return false
}

// String returns the lower_snake_case storage form.
func (r ParkReason) String() string { return string(r) }
