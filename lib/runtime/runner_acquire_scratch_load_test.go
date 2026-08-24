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
	scratch []byte
	err     error
}

func (q scratchLoadQueue) LoadScratch(_ context.Context, _ shared.UUID, _ persistence.Tx) ([]byte, error) {
	return q.scratch, q.err
}

// @concept: executor
func TestLoadScratchIntoAcquisition_FailsRatherThanHandOverAFalseEmptyState(t *testing.T) {
	t.Parallel()
	nodeRunID := shared.UUID(uuid.New())
	cand := persistence.Candidate{NodeRunID: nodeRunID}
	dbErr := errors.New("simulated scratch read failure")

	out := acquisition{}
	args := RunArgs{Queue: scratchLoadQueue{err: dbErr}}
	err := loadScratchIntoAcquisition(context.Background(), args, &out, cand, nil)
	if err == nil {
		t.Fatal("expected the dispatch to fail rather than hand the executor an empty scratch bag " +
			"it cannot distinguish from a genuine first run")
	}
	if !strings.Contains(err.Error(), "reading persisted scratch") {
		t.Fatalf("error %q does not name what went wrong", err)
	}
	if !strings.Contains(err.Error(), nodeRunID.String()) {
		t.Fatalf("error %q does not name the dispatch %s", err, nodeRunID)
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("error %v does not wrap the underlying cause %v", err, dbErr)
	}
	if out.Scratch != nil {
		t.Fatalf("a failed scratch load must leave the acquisition's scratch unset, got %q", out.Scratch)
	}
}

// @concept: executor
// @decision: scratch-column
func TestLoadScratchIntoAcquisition_LoadsScratchFromTheRow(t *testing.T) {
	t.Parallel()
	cand := persistence.Candidate{NodeRunID: shared.UUID(uuid.New())}
	out := acquisition{}
	args := RunArgs{Queue: scratchLoadQueue{scratch: []byte("row-scratch")}}
	if err := loadScratchIntoAcquisition(context.Background(), args, &out, cand, nil); err != nil {
		t.Fatalf("loadScratchIntoAcquisition: %v", err)
	}
	if string(out.Scratch) != "row-scratch" {
		t.Fatalf("scratch = %q, want %q", out.Scratch, "row-scratch")
	}
}
