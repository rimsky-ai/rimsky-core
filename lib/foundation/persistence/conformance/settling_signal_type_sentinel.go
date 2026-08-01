// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: node-run

package conformance

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testSettlingSignalTypeNullNotEmptyStringSentinel(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	var beforeTerminal *persistence.NodeRunLatest
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Nodes().GetLatestRunForNode(ctx, fix.NodeID, tx)
		beforeTerminal = r
		return err
	}); err != nil {
		t.Fatalf("GetLatestRunForNode before terminal: %v", err)
	}
	if beforeTerminal == nil {
		t.Fatalf("expected a run row before any terminal transition")
	}
	if beforeTerminal.SettlingSignalType != nil {
		t.Fatalf("settling_signal_type before any terminal transition = %q (non-nil), want nil "+
			"(a run with no settled signal must be NULL, never an empty-string sentinel)",
			*beforeTerminal.SettlingSignalType)
	}

	successType := "terminal/success"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().UpdateState(ctx, runID, cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
			return err
		}
		return store.Nodes().UpdateState(ctx, runID, cascade.NodeStateFresh, cascade.ReasonHandlerComplete, &successType, tx)
	}); err != nil {
		t.Fatalf("UpdateState to fresh with settling_signal_type: %v", err)
	}

	var afterSuccess *persistence.NodeRunLatest
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Nodes().GetLatestRunForNode(ctx, fix.NodeID, tx)
		afterSuccess = r
		return err
	}); err != nil {
		t.Fatalf("GetLatestRunForNode after success: %v", err)
	}
	if afterSuccess == nil {
		t.Fatalf("expected a run row after terminal transition")
	}
	if afterSuccess.SettlingSignalType == nil {
		t.Fatalf("settling_signal_type after a successful terminal transition = nil, want %q", successType)
	}
	if *afterSuccess.SettlingSignalType == "" {
		t.Fatalf("settling_signal_type after a successful terminal transition round-tripped as an " +
			"empty string; the column must carry the real typed value, never an empty-string sentinel")
	}
	if *afterSuccess.SettlingSignalType != successType {
		t.Fatalf("settling_signal_type after a successful terminal transition = %q, want %q",
			*afterSuccess.SettlingSignalType, successType)
	}
}
