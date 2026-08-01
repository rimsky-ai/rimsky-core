// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestApplyTerminalComplete_CommitWritebackSchemaViolation_RoutesToAttributesSchemaFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"foo": map[string]any{"type": "string"}},
		"required":   []any{"foo"},
	}

	args, acq, tables := seedPoisonPortfolioFixture(t, spec.ClaimHandleStateCommitted)

	badDelta := map[string]any{"foo": 123}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalComplete(ctx, args, acq, map[string]any{}, schema,
			terminalEvent{Kind: terminalKindComplete, Changed: true, AttributesDel: badDelta}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalComplete: %v", err)
	}

	var runRow *persistence.NodeRunForGate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runRow == nil {
		t.Fatalf("node run %s missing after applyTerminalComplete", acq.NodeRunID)
	}
	if runRow.State != cascade.NodeStateFailed {
		t.Fatalf("run state = %v, want %v (commit-time schema validation on the executor write-back must reject an invalid merged delta even though dispatch-time validation already ran)",
			runRow.State, cascade.NodeStateFailed)
	}

	var events persistence.EventListResult
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Events().List(ctx, persistence.EventListFilter{NodeID: &acq.NodeID}, persistence.ListPagination{Limit: 100}, tx)
		events = r
		return err
	}); err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, e := range events.Events {
		if e.KindRaw == "attributes_schema_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an attributes_schema_failed event to be appended, got %+v", events.Events)
	}
}

func TestApplyTerminalComplete_CommitWritebackSchemaSatisfied_Succeeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"foo": map[string]any{"type": "string"}},
		"required":   []any{"foo"},
	}

	args, acq, tables := seedPoisonPortfolioFixture(t, spec.ClaimHandleStateCommitted)

	goodDelta := map[string]any{"foo": "bar"}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalComplete(ctx, args, acq, map[string]any{}, schema,
			terminalEvent{Kind: terminalKindComplete, Changed: true, AttributesDel: goodDelta}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalComplete: %v", err)
	}

	var runRow *persistence.NodeRunForGate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runRow == nil || runRow.State != cascade.NodeStateFresh {
		t.Fatalf("run state = %+v, want %v (control: a schema-satisfying write-back must not be rejected by the commit-time gate)",
			runRow, cascade.NodeStateFresh)
	}
}
