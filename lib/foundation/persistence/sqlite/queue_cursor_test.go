// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func cursorCandidate(at time.Time, sequence int64, id shared.UUID) persistence.Candidate {
	return persistence.Candidate{NodeRunID: id, EnqueuedAt: at, Sequence: sequence}
}

func TestCandidateAfterCursor_ZeroTimestampStillHonoursTheSequenceCursor(t *testing.T) {
	req := persistence.SelectCandidatesRequest{CursorAfterSequence: 7}
	behind := cursorCandidate(time.Time{}, 3, shared.UUID{})
	ahead := cursorCandidate(time.Time{}, 9, shared.UUID{})
	if candidateAfterCursor(behind, req) {
		t.Fatalf("the driver must skip a candidate behind the sequence cursor even when the cursor timestamp is zero")
	}
	if !candidateAfterCursor(ahead, req) {
		t.Fatalf("the driver must page a candidate ahead of the sequence cursor even when the cursor timestamp is zero")
	}
}

func TestCandidateAfterCursor_FullyZeroCursorAcceptsEverything(t *testing.T) {
	req := persistence.SelectCandidatesRequest{}
	if !candidateAfterCursor(cursorCandidate(time.Unix(0, 1).UTC(), 0, shared.UUID{1}), req) {
		t.Fatalf("the driver must accept the first page under a fully zero cursor")
	}
}

func TestCandidateAfterCursor_TiesBreakOnNodeRunID(t *testing.T) {
	at := time.Unix(1000, 0).UTC()
	req := persistence.SelectCandidatesRequest{
		CursorEnqueuedAfter:  at,
		CursorAfterSequence:  4,
		CursorAfterNodeRunID: shared.UUID{5},
	}
	if candidateAfterCursor(cursorCandidate(at, 4, shared.UUID{5}), req) {
		t.Fatalf("the cursor row itself must not repeat")
	}
	if !candidateAfterCursor(cursorCandidate(at, 4, shared.UUID{6}), req) {
		t.Fatalf("the driver must page a higher node-run id at the same timestamp and sequence")
	}
}
