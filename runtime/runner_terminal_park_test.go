// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"
	"time"

	signalpkg "github.com/fallguy/rimsky/foundation/signal"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func TestParkTerminalSignal_Snooze(t *testing.T) {
	resume := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	ev := terminalEvent{
		ParkReason:       genv1.ParkReason_PARK_REASON_SNOOZE,
		ParkResumeAt:     resume,
		ParkSessionToken: "tok",
		ParkPayload:      []byte("hi"),
		ParkReasonLabel:  "wait",
		ParkReasonNote:   "ten min",
	}
	got := parkTerminalSignal(ev)
	if got.Type != signalpkg.TypePath("terminal/park/snooze") {
		t.Fatalf("snooze type: got %q", got.Type)
	}
	if got.Payload["resume_at"].(time.Time) != resume {
		t.Fatalf("resume_at: got %v", got.Payload["resume_at"])
	}
	if got.Payload["session_token"].(string) != "tok" {
		t.Fatalf("session_token: got %v", got.Payload["session_token"])
	}
	if got.Payload["parked_reason_label"].(string) != "wait" {
		t.Fatalf("parked_reason_label: got %v", got.Payload["parked_reason_label"])
	}
}

func TestParkTerminalSignal_AwaitCallback(t *testing.T) {
	ev := terminalEvent{
		ParkReason:      genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK,
		ParkReasonLabel: "wait_for_remote",
	}
	got := parkTerminalSignal(ev)
	if got.Type != signalpkg.TypePath("terminal/park/await_callback") {
		t.Fatalf("await_callback type: got %q", got.Type)
	}
	// Zero ParkResumeAt → nil pointer in payload.
	if got.Payload["resume_at"] != nil {
		t.Fatalf("resume_at should be nil when zero: got %v", got.Payload["resume_at"])
	}
	if got.Payload["parked_reason_label"].(string) != "wait_for_remote" {
		t.Fatalf("parked_reason_label: got %v", got.Payload["parked_reason_label"])
	}
}
