// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"
	"time"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

func TestParkTerminalSignal(t *testing.T) {
	resume := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	ev := terminalEvent{
		ParkResumeAt: resume,
		Tags:         []string{"await_remote"},
	}
	got := parkTerminalSignal(ev)
	if got.Type != signalpkg.TypePath("transient/park") {
		t.Fatalf("park type: got %q", got.Type)
	}
	if got.Payload["resume_at"].(time.Time) != resume {
		t.Fatalf("resume_at: got %v", got.Payload["resume_at"])
	}
	tags := got.Payload["tags"].([]string)
	if len(tags) != 1 || tags[0] != "await_remote" {
		t.Fatalf("tags: got %v", tags)
	}
	if _, present := got.Payload["parked_reason_label"]; present {
		t.Fatalf("parked_reason_label must not appear in the park payload")
	}
	if _, present := got.Payload["parked_reason_note"]; present {
		t.Fatalf("parked_reason_note must not appear in the park payload")
	}
}
