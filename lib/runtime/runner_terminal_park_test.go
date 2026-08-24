// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	if got.Payload.Map()["resume_at"].(string) != resume.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("resume_at: got %v", got.Payload.Map()["resume_at"])
	}
	tags := got.Payload.Map()["tags"].([]any)
	if len(tags) != 1 || tags[0] != "await_remote" {
		t.Fatalf("tags: got %v", tags)
	}
	if _, present := got.Payload.Map()["parked_reason_label"]; present {
		t.Fatalf("parked_reason_label must not appear in the park payload")
	}
	if _, present := got.Payload.Map()["parked_reason_note"]; present {
		t.Fatalf("parked_reason_note must not appear in the park payload")
	}
}

func TestParkTerminalSignal_RecordsScratchSizeNeverBytes(t *testing.T) {
	scratch := []byte("resume-context-carry-forward-bytes")

	t.Run("scratch present: size recorded", func(t *testing.T) {
		got := parkTerminalSignal(terminalEvent{
			ParkResumeAt: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
			Scratch:      scratch,
		})
		assertParkScratchAudit(t, got, len(scratch))
	})

	t.Run("no scratch: zero size", func(t *testing.T) {
		got := parkTerminalSignal(terminalEvent{
			ParkResumeAt: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
		})
		assertParkScratchAudit(t, got, 0)
	})
}

func assertParkScratchAudit(t *testing.T, sig signalpkg.Signal, wantSize int) {
	t.Helper()
	size, ok := sig.Payload.Map()["scratch_size"].(float64)
	if !ok || int(size) != wantSize {
		t.Fatalf("scratch_size: got %v want %d", sig.Payload.Map()["scratch_size"], wantSize)
	}
	for k := range sig.Payload.Map() {
		if k == "scratch" || k == "scratch_bytes" || k == "payload" {
			t.Fatalf("park audit payload must never carry raw scratch/payload bytes, found key %q", k)
		}
	}
}
