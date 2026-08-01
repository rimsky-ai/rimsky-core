// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scheduler

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func ListPureCascadeReady(ctx context.Context, persist persistence.Tables) ([]persistence.PureCascadeReadyRow, error) {
	var ready []persistence.PureCascadeReadyRow
	if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := persist.Nodes().ListPureCascadeReady(ctx, tx)
		ready = rows
		return err
	}); err != nil {
		return nil, err
	}
	return ready, nil
}

func AcquiresClaims(def *nodepkg.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return len(def.ClaimProducers) > 0
}

func PrepareNativeClaimRouting(ctx context.Context, persist persistence.Tables, n persistence.PureCascadeReadyRow, def *nodepkg.TemplateNodeDef) (bool, error) {
	required := nodepkg.RequiredClaimProducers(*def)
	if required == nil {
		required = []string{}
	}
	var prepared bool
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := persist.Nodes().SetRunRequiredClaimProducers(ctx, n.NodeRunID, required, tx)
		prepared = ok
		return err
	})
	return prepared, err
}
