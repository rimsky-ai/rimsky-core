// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type queueFailingRemoveForNode struct {
	persistence.Queue
	err error
}

func (q queueFailingRemoveForNode) RemoveForNodeInTx(context.Context, shared.UUID, shared.UUID, string, persistence.Tx) error {
	return q.err
}

func TestApplyTerminalComplete_MidTransactionFailureRollsBackVerdictAndAttributes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	args, acq, tables := seedRunningNodeForParkFixture(t)

	injected := errors.New("injected failure after settling verdict and attributes_delta are staged")
	args.Queue = queueFailingRemoveForNode{Queue: args.Queue, err: injected}

	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalComplete(ctx, args, acq, nil, nil, terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       true,
			AttributesDel: map[string]any{"result_key": "result_value"},
			Tags:          []string{"terminal-tag-marker"},
		}, tx)
		return err
	})
	if err == nil {
		t.Fatalf("applyTerminalComplete: expected the injected RemoveForNodeInTx failure to propagate, got nil")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("applyTerminalComplete error = %v; want it to wrap the injected failure %v", err, injected)
	}

	var runRow *persistence.NodeRunForGate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, gerr := tables.Nodes().GetRunForGate(ctx, tx, acq.NodeRunID)
		runRow = r
		return gerr
	}); err != nil {
		t.Fatalf("load run after rollback: %v", err)
	}
	if runRow == nil {
		t.Fatalf("node run %s missing after rolled-back applyTerminalComplete", acq.NodeRunID)
	}
	if runRow.State == cascade.NodeStateFresh {
		t.Fatalf("settling verdict (state=fresh) survived a transaction that failed downstream in the same tx; "+
			"applyTerminalComplete's state update, attribute writeback, and later steps must be one atomic unit, got state=%v", runRow.State)
	}

	var attrRow *persistence.NodeAttributesRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, gerr := tables.NodeAttributes().GetByRun(ctx, acq.NodeRunID, tx)
		attrRow = r
		return gerr
	}); err != nil {
		t.Fatalf("load attributes after rollback: %v", err)
	}
	if attrRow != nil {
		if _, ok := attrRow.Data["result_key"]; ok {
			t.Fatalf("attributes_delta write survived a transaction that failed downstream in the same tx: %+v", attrRow.Data)
		}
	}
}
