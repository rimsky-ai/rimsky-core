// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type scratchLoadQueue struct {
	persistence.Queue
	inline  []byte
	handle  string
	backend string
	err     error
}

func (q scratchLoadQueue) LoadScratch(_ context.Context, _ shared.UUID, _ persistence.Tx) ([]byte, string, string, error) {
	return q.inline, q.handle, q.backend, q.err
}

type scratchLoadBlobBackend struct {
	persistence.BlobBackend
	name string
	body []byte
	err  error
}

func (b scratchLoadBlobBackend) Name() string { return b.name }

func (b scratchLoadBlobBackend) Read(context.Context, persistence.Handle) ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.body, nil
}

// @concept: executor
func TestLoadScratchIntoAcquisition_FailsRatherThanHandOverAFalseEmptyState(t *testing.T) {
	t.Parallel()
	nodeRunID := shared.UUID(uuid.New())
	cand := persistence.Candidate{NodeRunID: nodeRunID}
	readErr := errors.New("simulated blob read failure")
	dbErr := errors.New("simulated scratch read failure")

	cases := []struct {
		name     string
		args     RunArgs
		wantWord string
		wantWrap error
	}{
		{
			name:     "database read fails",
			args:     RunArgs{Queue: scratchLoadQueue{err: dbErr}},
			wantWord: "reading persisted scratch",
			wantWrap: dbErr,
		},
		{
			name:     "spilled scratch with no blob backend configured",
			args:     RunArgs{Queue: scratchLoadQueue{handle: "h-1", backend: "s3"}},
			wantWord: `active blob backend is "<none>"`,
		},
		{
			name: "spilled scratch whose backend does not match the configured one",
			args: RunArgs{
				Queue: scratchLoadQueue{handle: "h-1", backend: "s3"},
				Blob:  scratchLoadBlobBackend{name: "memory"},
			},
			wantWord: `active blob backend is "memory"`,
		},
		{
			name: "spilled scratch whose blob read fails",
			args: RunArgs{
				Queue: scratchLoadQueue{handle: "h-1", backend: "memory"},
				Blob:  scratchLoadBlobBackend{name: "memory", err: readErr},
			},
			wantWord: "reading spilled scratch",
			wantWrap: readErr,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := acquisition{}
			err := loadScratchIntoAcquisition(context.Background(), tc.args, &out, cand, nil)
			if err == nil {
				t.Fatal("expected the dispatch to fail rather than hand the executor an empty scratch bag " +
					"it cannot distinguish from a genuine first run")
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Fatalf("error %q does not name what went wrong (want it to contain %q)", err, tc.wantWord)
			}
			if !strings.Contains(err.Error(), nodeRunID.String()) {
				t.Fatalf("error %q does not name the dispatch %s", err, nodeRunID)
			}
			if tc.wantWrap != nil && !errors.Is(err, tc.wantWrap) {
				t.Fatalf("error %v does not wrap the underlying cause %v", err, tc.wantWrap)
			}
			if out.Scratch != nil {
				t.Fatalf("a failed scratch load must leave the acquisition's scratch unset, got %q", out.Scratch)
			}
		})
	}
}

// @concept: executor
func TestLoadScratchIntoAcquisition_LoadsInlineAndSpilledScratch(t *testing.T) {
	t.Parallel()
	cand := persistence.Candidate{NodeRunID: shared.UUID(uuid.New())}

	t.Run("inline", func(t *testing.T) {
		t.Parallel()
		out := acquisition{}
		args := RunArgs{Queue: scratchLoadQueue{inline: []byte("inline-scratch")}}
		if err := loadScratchIntoAcquisition(context.Background(), args, &out, cand, nil); err != nil {
			t.Fatalf("loadScratchIntoAcquisition: %v", err)
		}
		if string(out.Scratch) != "inline-scratch" {
			t.Fatalf("scratch = %q, want %q", out.Scratch, "inline-scratch")
		}
	})

	t.Run("spilled", func(t *testing.T) {
		t.Parallel()
		out := acquisition{}
		args := RunArgs{
			Queue: scratchLoadQueue{handle: "h-1", backend: "memory"},
			Blob:  scratchLoadBlobBackend{name: "memory", body: []byte("spilled-scratch")},
		}
		if err := loadScratchIntoAcquisition(context.Background(), args, &out, cand, nil); err != nil {
			t.Fatalf("loadScratchIntoAcquisition: %v", err)
		}
		if string(out.Scratch) != "spilled-scratch" {
			t.Fatalf("scratch = %q, want %q", out.Scratch, "spilled-scratch")
		}
	})
}
