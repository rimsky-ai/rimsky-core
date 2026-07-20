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
	got := parkTerminalSignal(RunArgs{}, ev)
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

func TestParkTerminalSignal_RecordsScratchSizeAndSpillFlagNeverBytes(t *testing.T) {
	scratch := []byte("resume-context-carry-forward-bytes")
	ev := terminalEvent{
		ParkResumeAt: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
		Scratch:      scratch,
	}

	t.Run("below spill threshold: inline, not spilled", func(t *testing.T) {
		got := parkTerminalSignal(RunArgs{
			Blob:               failingWriteBlobBackend{},
			BlobSpillThreshold: len(scratch) + 1,
		}, ev)
		assertParkScratchAudit(t, got, len(scratch), false)
	})

	t.Run("above spill threshold: spilled", func(t *testing.T) {
		got := parkTerminalSignal(RunArgs{
			Blob:               failingWriteBlobBackend{},
			BlobSpillThreshold: 1,
		}, ev)
		assertParkScratchAudit(t, got, len(scratch), true)
	})

	t.Run("no scratch: zero size, not spilled", func(t *testing.T) {
		got := parkTerminalSignal(RunArgs{
			Blob:               failingWriteBlobBackend{},
			BlobSpillThreshold: 1,
		}, terminalEvent{ParkResumeAt: ev.ParkResumeAt})
		assertParkScratchAudit(t, got, 0, false)
	})
}

func assertParkScratchAudit(t *testing.T, sig signalpkg.Signal, wantSize int, wantSpilled bool) {
	t.Helper()
	size, ok := sig.Payload["scratch_size"].(int)
	if !ok || size != wantSize {
		t.Fatalf("scratch_size: got %v want %d", sig.Payload["scratch_size"], wantSize)
	}
	spilled, ok := sig.Payload["scratch_spilled"].(bool)
	if !ok || spilled != wantSpilled {
		t.Fatalf("scratch_spilled: got %v want %v", sig.Payload["scratch_spilled"], wantSpilled)
	}
	for k := range sig.Payload {
		if k == "scratch" || k == "scratch_bytes" || k == "payload" {
			t.Fatalf("park audit payload must never carry raw scratch/payload bytes, found key %q", k)
		}
	}
}
