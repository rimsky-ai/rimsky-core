// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestApplyTerminalError_RetryLeavesPersistedAttributeBagUnchanged(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	maxRetries := 3
	acq.NodeDef = &node.TemplateNodeDef{
		Type:     "parker",
		Executor: "test-executor",
		ErrorTypes: map[string]spec.ErrorTypePolicy{
			"transient": {Action: spec.ActionRetry},
		},
		MaxRetries: &maxRetries,
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalError(ctx, args, acq,
			map[string]any{"existing": "value"}, nil,
			"transient", map[string]any{"error": "boom"}, nil,
			map[string]any{"foo": "bar"}, nil, tx)
		return err
	}))

	require.NotNil(t, acq.RetryDecision)
	require.True(t, acq.RetryDecision.IsRetry(),
		"fixture must resolve to a retry outcome for this test to exercise the retry-vs-settle ordering")

	var bag *persistence.NodeAttributesRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := tables.NodeAttributes().GetByRun(ctx, acq.NodeRunID, tx)
		bag = row
		return err
	}))
	if bag != nil {
		_, hasDelta := bag.Data["foo"]
		require.False(t, hasDelta,
			"the persisted attribute bag must stay unchanged across a retry: an errored terminal "+
				"carrying attributes_delta must not upsert it into the row until the error policy "+
				"resolves to a non-retry (settling) outcome")
		_, hasResolved := bag.Data["existing"]
		require.False(t, hasResolved,
			"a retry must not upsert the resolved-attrs+delta merge into the persisted bag either")
	}
}
