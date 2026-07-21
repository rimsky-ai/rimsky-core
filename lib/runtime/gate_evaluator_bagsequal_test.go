// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedriver "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

const bagsEqualTestTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func openSQLiteForBagsEqual(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "bagsequal.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestBagsEqual_ResolvesOnDemandWhenPriorDispatchBagIsNil(t *testing.T) {
	ctx := context.Background()
	d := openSQLiteForBagsEqual(t)
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	priorRunID := shared.UUID(uuid.New())
	var priorRunFrameID shared.UUID

	tmpl := node.TemplateSpec{
		Name: "bags-equal-on-demand", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "n", Executor: "stub",
				Attributes: &tmplspec.NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"seed": map[string]any{
								"type":   "string",
								"source": "{{params.seed}}",
							},
						},
						"required": []any{"seed"},
					},
				},
			},
		},
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := tables.Templates().UpdateState(ctx, templateHash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
			Params: map[string]any{"seed": "abc"},
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: "n", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/fixture-seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		priorRunFrameID = frameID
		return nil
	}))

	rawDB, ok := sqlitedriver.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	_, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_claim_producers, enqueued_at, frame_id,
		   run_scope_id, state, creation_reason, sequence
		 ) VALUES (?, ?, 'stub', '[]', ?, ?, ?, 'stale', 'cascade', 1)`,
		priorRunID.String(), nodeID.String(), time.Now().UTC().Format(bagsEqualTestTimeLayout),
		priorRunFrameID.String(), mainScopeID.String(),
	)
	require.NoError(t, err, "seed prior run row directly, bypassing SnapshotBagForNewRun so no "+
		"rimsky_node_attributes row (and thus no dispatch_input_bag) is ever created for it")

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		bag, err := tables.NodeAttributes().GetDispatchInputBag(ctx, priorRunID, tx)
		require.NoError(t, err)
		require.Nil(t, bag, "fixture bug: prior run must have no dispatch_input_bag row yet")
		return nil
	}))

	args := RunArgs{
		Persist: tables,
		Logger:  shared.SilentLogger{},
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		equal, err := bagsEqual(ctx, args, priorRunID, map[string]any{"seed": "abc"}, tx)
		require.NoError(t, err)
		require.True(t, equal,
			"bagsEqual must fall back to on-demand resolution (GetRunForGate + buildResolvedBagAtGateEvalCarry) "+
				"when the prior run's dispatch_input_bag column is nil, recomputing the prior run's bag from its "+
				"own template/params rather than treating a missing snapshot as never-equal")
		return nil
	}))

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		equal, err := bagsEqual(ctx, args, priorRunID, map[string]any{"seed": "different"}, tx)
		require.NoError(t, err)
		require.False(t, equal,
			"the on-demand resolution path must perform a genuine comparison, not unconditionally report "+
				"equal: a bag that legitimately differs from the on-demand-recomputed prior bag must compare unequal")
		return nil
	}))

	failingArgs := RunArgs{
		Persist: erroringTemplatesTables{Tables: tables},
		Logger:  shared.SilentLogger{},
	}
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := bagsEqual(ctx, failingArgs, priorRunID, map[string]any{"seed": "abc"}, tx)
		require.ErrorIs(t, err, errBagsEqualTemplateLoad,
			"an on-demand resolve failure must propagate to the caller, not silently report the bags as unequal")
		return nil
	}))
}

var errBagsEqualTemplateLoad = errors.New("template load failed")

type erroringTemplateTable struct {
	persistence.TemplateTable
}

func (e erroringTemplateTable) GetByHash(
	ctx context.Context, hash string, tx persistence.Tx,
) (*persistence.TemplateRow, error) {
	return nil, errBagsEqualTemplateLoad
}

type erroringTemplatesTables struct {
	persistence.Tables
}

func (e erroringTemplatesTables) Templates() persistence.TemplateTable {
	return erroringTemplateTable{e.Tables.Templates()}
}
