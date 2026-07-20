// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func openSQLiteForGateEvalSchema(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "gateeval-schema.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedGateEvalSchemaFixture(
	t *testing.T, tmpl node.TemplateSpec, executor string,
) (persistence.Tables, *persistence.NodeRunForGate) {
	t.Helper()
	ctx := context.Background()
	d := openSQLiteForGateEvalSchema(t)
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	var frameID shared.UUID

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := tables.Templates().UpdateState(ctx, templateHash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: tmpl.Nodes[0].Type, Executor: executor,
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/fixture-seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))

	row := &persistence.NodeRunForGate{
		NodeID:     nodeID,
		RunScopeID: mainScopeID,
		FrameID:    frameID,
	}
	return tables, row
}

func TestBuildResolvedBagAtGateEvalCarry_ExecSchemaOnlyNodeUsesExecutorSchema(t *testing.T) {
	ctx := context.Background()
	tmpl := node.TemplateSpec{
		Name: "gate-eval-exec-schema-only", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n", Executor: "stub-exec"},
		},
	}
	tables, row := seedGateEvalSchemaFixture(t, tmpl, "stub-exec")

	args := RunArgs{
		Persist: tables,
		Logger:  shared.SilentLogger{},
		ExpectedAttributesSchemaFor: func(executorName string) ([]byte, bool) {
			if executorName != "stub-exec" {
				return nil, false
			}
			return []byte(`{"type":"object","properties":{"greeting":{"type":"string","default":"hi"}}}`), true
		},
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resolved, err := buildResolvedBagAtGateEvalCarry(ctx, args, tx, row, map[string]any{})
		require.NoError(t, err)
		require.Equal(t, "hi", resolved["greeting"],
			"a node with no declared attributes block must still resolve the executor's expected-attributes "+
				"schema at gate-eval, matching dispatch-time computeEffectiveAttributeSchema — not early-return {}")
		return nil
	}))
}

func TestBuildResolvedBagAtGateEvalCarry_IncludesTemplateAttributeDefaults(t *testing.T) {
	ctx := context.Background()
	tmpl := node.TemplateSpec{
		Name: "gate-eval-template-defaults", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "n", Executor: "stub-exec",
				Attributes: &tmplspec.NodeAttributesDef{
					Schema: map[string]any{"type": "object", "properties": map[string]any{}},
				},
			},
		},
		Defaults: &tmplspec.TemplateDefaults{
			Attributes: &tmplspec.TemplateAttributeDefaults{
				ByExecutor: map[string]map[string]any{
					"stub-exec": {"region": "us-east"},
				},
			},
		},
	}
	tables, row := seedGateEvalSchemaFixture(t, tmpl, "stub-exec")

	args := RunArgs{
		Persist: tables,
		Logger:  shared.SilentLogger{},
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resolved, err := buildResolvedBagAtGateEvalCarry(ctx, args, tx, row, map[string]any{})
		require.NoError(t, err)
		require.Equal(t, "us-east", resolved["region"],
			"gate-eval schema assembly must include template-level attribute defaults for the executor, "+
				"matching dispatch-time computeEffectiveAttributeSchema which passes acq.TemplateAttributeDefaults "+
				"instead of a hardcoded nil")
		return nil
	}))
}
