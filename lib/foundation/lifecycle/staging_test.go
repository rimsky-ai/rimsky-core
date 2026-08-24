// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lifecycle_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func openStagingDatabase(t *testing.T) persistence.Tables {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "staging.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })
	return d.Tables()
}

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func TestStageRunScopeTerminal_StagesOneRowPerSubscribingServiceOfTheInstancesTemplate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tables := openStagingDatabase(t)

	templateHash := "sha256-run-scope-terminal"
	sp := spec.TemplateSpec{
		Name: "run-scope-terminal", Version: "v1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "n1", Executor: "beta"},
			{Type: "n2", ClaimProducers: []spec.NodeClaimProducerRef{{Name: "alpha"}}},
		},
	}
	instanceID := shared.UUID(uuid.New())
	runScopeID := shared.UUID(uuid.New())
	instanceKey := "ck-" + uuid.NewString()

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: sp, State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: runScopeID, GraphName: "main", InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		_, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, InstanceKey: &instanceKey,
			TargetRoutingIdentity: "test-daemon", Params: map[string]any{},
		}, tx)
		return err
	}))

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageRunScopeTerminal(ctx, tables, instanceID, runScopeID, "frame_end", nil, tx)
	}))

	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleScopeRunScope, runScopeID.String(), tx)
		rows = r
		return err
	}))

	require.Len(t, rows, 2, "the closed scope owes one row per service its instance's template subscribes to")
	services := []string{rows[0].ClaimProducerName, rows[1].ClaimProducerName}
	require.Equal(t, []string{"alpha", "beta"}, services)

	for _, row := range rows {
		require.Equal(t, lifecycle.EventRunScopeTerminal.String(), row.Event)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(row.Payload, &payload))
		require.Equal(t, map[string]any{
			"run_scope_id":    runScopeID.String(),
			"instance_id":     instanceID.String(),
			"terminal_reason": "frame_end",
		}, payload, "a run-scope terminal row carries the scope, the instance, and the reason, and nothing more")
	}
}

// @concept: run-scope
func TestStageRunScopeTerminal_RefusesAnInstanceThatIsGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tables := openStagingDatabase(t)

	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageRunScopeTerminal(ctx, tables,
			shared.UUID(uuid.New()), shared.UUID(uuid.New()), "frame_end", nil, tx)
	})
	require.ErrorIs(t, err, persistence.ErrNotFound,
		"a missing instance fails the caller's transaction, so the transition never commits without its rows")
}
