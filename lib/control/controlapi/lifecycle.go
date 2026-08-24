// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber

package controlapi

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func logFanOutFailures(deps AppDeps, msg string, perServiceErr map[string]error, err error, kv ...any) {
	if deps.Logger == nil {
		return
	}
	if err != nil {
		deps.Logger.Warn(msg, append(kv, "error", err.Error())...)
	}
	for name, perr := range perServiceErr {
		deps.Logger.Warn(msg, append(kv, "service", name, "error", perr.Error())...)
	}
}

// @concept: run-scope
// @concept: frame
// @decision: lifecycle-fanout-after-commit
func closeAndStageRunScopeTerminalsInTx(
	ctx context.Context,
	deps AppDeps,
	instanceID shared.UUID,
	terminalReason string,
	tx persistence.Tx,
) ([]shared.UUID, error) {
	pag := persistence.ListPagination{Limit: 256}
	instArg := instanceID
	filter := persistence.FrameListFilter{InstanceID: &instArg}
	seenRoots := map[shared.UUID]struct{}{}
	var closed []shared.UUID
	for {
		page, err := deps.Persist.Frames().ListForObservability(ctx, filter, pag, tx)
		if err != nil {
			return nil, fmt.Errorf("list frames of instance %s: %w", instanceID, err)
		}
		for _, f := range page.Rows {
			root := f.RootRunScopeID
			if root == (shared.UUID{}) {
				continue
			}
			if _, dup := seenRoots[root]; dup {
				continue
			}
			seenRoots[root] = struct{}{}
			scopes, err := closeAndStageScopeTreeInTx(ctx, deps, instanceID, root, terminalReason, tx)
			if err != nil {
				return nil, err
			}
			closed = append(closed, scopes...)
		}
		if page.NextCursor == "" {
			return closed, nil
		}
		pag.Cursor = page.NextCursor
	}
}

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func closeAndStageScopeTreeInTx(
	ctx context.Context,
	deps AppDeps,
	instanceID, rootRunScopeID shared.UUID,
	terminalReason string,
	tx persistence.Tx,
) ([]shared.UUID, error) {
	tree, err := deps.Persist.RunScopes().ListTreeDeepestFirst(ctx, rootRunScopeID, tx)
	if err != nil {
		return nil, fmt.Errorf("list run-scope tree %s: %w", rootRunScopeID, err)
	}
	closed := make([]shared.UUID, 0, len(tree))
	for _, scope := range tree {
		if scope.ClosedAt != nil {
			continue
		}
		if err := deps.Persist.RunScopes().Close(ctx, scope.ID, tx); err != nil {
			return nil, fmt.Errorf("close run scope %s: %w", scope.ID, err)
		}
		if err := lifecycle.StageRunScopeTerminal(ctx, deps.Persist, instanceID, scope.ID,
			terminalReason, deps.LateBindServiceProxies, tx); err != nil {
			return nil, err
		}
		closed = append(closed, scope.ID)
	}
	return closed, nil
}

// @concept: instance
// @decision: lifecycle-fanout-after-commit
func stageInstanceTerminatedInTx(
	ctx context.Context,
	deps AppDeps,
	inst persistence.InstanceRow,
	terminatedAtUnixMs int64,
	tx persistence.Tx,
) error {
	tpl, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil {
		return fmt.Errorf("load template %s of instance %s: %w", inst.TemplateHash, inst.ID, err)
	}
	if tpl == nil {
		return fmt.Errorf("template %s of instance %s: %w", inst.TemplateHash, inst.ID, persistence.ErrNotFound)
	}
	return lifecycle.StageInstanceEvent(ctx, deps.Persist.LifecycleOutbox(), lifecycle.EventInstanceTerminated,
		inst.TemplateHash, inst.ID.String(), tpl.Spec, deps.LateBindServiceProxies,
		lifecycle.InstancePayload{TerminatedAtUnixMs: terminatedAtUnixMs}, tx)
}
