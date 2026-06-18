// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// @concept: parked-state
type ParkReason string

const (
	ParkReasonUnspecified ParkReason = ""
	ParkReasonAwaitCallback ParkReason = "await_callback"
	ParkReasonSnooze ParkReason = "snooze"
)

func (r ParkReason) IsValid() bool {
	switch r {
	case ParkReasonAwaitCallback, ParkReasonSnooze:
		return true
	}
	return false
}

func (r ParkReason) String() string { return string(r) }
