// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @concept: run-scope
func FrameRunScopeTerminalFanout(
	persist persistence.Tables,
	lifecycleSubs *locks.LifecycleRegistry,
	peersForSpec func(tplSpec node.TemplateSpec) []string,
) frame.RunScopeTerminalFanout {
	if lifecycleSubs == nil || peersForSpec == nil {
		return nil
	}
	return func(ctx context.Context, tx persistence.Tx, instanceID, runScopeID shared.UUID, terminalReason string) {
		inst, err := persist.Instances().Get(ctx, instanceID, tx)
		if err != nil || inst == nil {
			slog.Warn("FrameRunScopeTerminalFanout: instance lookup failed; peers not notified",
				"instance_id", instanceID.String(), "run_scope_id", runScopeID.String(), "error", err)
			return
		}
		tpl, err := persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		if err != nil || tpl == nil {
			slog.Warn("FrameRunScopeTerminalFanout: template lookup failed; peers not notified",
				"instance_id", instanceID.String(), "run_scope_id", runScopeID.String(), "error", err)
			return
		}
		FanOutRunScopeEvent(ctx, persist, lifecycleSubs, peersForSpec,
			tpl.Spec, runScopeID, instanceID, terminalReason, tx)
	}
}

func FanOutRunScopeEvent(
	ctx context.Context,
	persist persistence.Tables,
	lifecycleSubs *locks.LifecycleRegistry,
	peersForSpec func(tplSpec node.TemplateSpec) []string,
	tplSpec node.TemplateSpec,
	runScopeID shared.UUID,
	instanceID shared.UUID,
	terminalReason string,
	tx persistence.Tx,
) {
	if lifecycleSubs == nil || peersForSpec == nil {
		return
	}
	peers := peersForSpec(tplSpec)
	scopeID := runScopeID.String()

	for _, name := range peers {
		existing, err := persist.LifecycleIdempotency().Get(
			ctx, name,
			persistence.LifecycleIdempotencyScopeRunScope,
			scopeID, tx,
		)
		if err != nil {
			slog.Warn("FanOutRunScopeEvent: lifecycle row lookup failed; skipping peer",
				"peer", name, "run_scope_id", scopeID, "error", err)
			continue
		}
		if existing != nil && existing.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
			continue
		}

		sub, ok := lifecycleSubs.Get(name)
		if !ok {
			continue
		}

		req := locks.OnRunScopeTerminalRequest{
			RunScopeID:     scopeID,
			TerminalReason: terminalReason,
			InstanceID:     instanceID.String(),
		}
		if err := sub.OnRunScopeTerminal(ctx, req); err != nil {
			continue
		}

		if err := persist.LifecycleIdempotency().Upsert(ctx,
			persistence.LifecycleIdempotencyRow{
				StoreRegistrationName: name,
				ScopeKind:             persistence.LifecycleIdempotencyScopeRunScope,
				ScopeID:               scopeID,
				State:                 persistence.LifecycleIdempotencyStateRunScopeTerminal,
			}, tx,
		); err != nil {
			slog.Warn("FanOutRunScopeEvent: lifecycle row upsert failed; close stands",
				"peer", name, "run_scope_id", scopeID, "error", err)
			continue
		}
	}
}
