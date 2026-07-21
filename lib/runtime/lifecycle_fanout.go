// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @concept: run-scope
func FrameRunScopeTerminalFanout(
	persist persistence.Tables,
	advLock persistence.AdvisoryLocker,
	lifecycleSubs *lifecycle.Registry,
	peersForSpec func(tplSpec node.TemplateSpec) []string,
) frame.RunScopeTerminalFanout {
	if lifecycleSubs == nil || peersForSpec == nil || advLock == nil {
		return nil
	}
	return func(ctx context.Context, instanceID, runScopeID shared.UUID, terminalReason string, tx persistence.Tx) {
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
		FanOutRunScopeEvent(ctx, persist, advLock, lifecycleSubs, peersForSpec,
			tpl.Spec, runScopeID, instanceID, terminalReason, nil, tx)
	}
}

func warnFanOut(logger shared.Logger, msg string, kv ...any) {
	if logger != nil {
		logger.Warn(msg, kv...)
		return
	}
	slog.Warn(msg, kv...)
}

func withOptionalFanOutTx(
	ctx context.Context, persist persistence.Tables, fn func(ctx context.Context, tx persistence.Tx) error, tx persistence.Tx,
) error {
	if tx != nil {
		return fn(ctx, tx)
	}
	return persist.Transaction(ctx, fn)
}

// @concept: run-scope
// @concept: lifecycle-subscriber
func FanOutRunScopeEvent(
	ctx context.Context,
	persist persistence.Tables,
	advLock persistence.AdvisoryLocker,
	lifecycleSubs *lifecycle.Registry,
	peersForSpec func(tplSpec node.TemplateSpec) []string,
	tplSpec node.TemplateSpec,
	runScopeID shared.UUID,
	instanceID shared.UUID,
	terminalReason string,
	logger shared.Logger,
	tx persistence.Tx,
) {
	if lifecycleSubs == nil || peersForSpec == nil {
		return
	}
	if advLock == nil {
		warnFanOut(logger, "FanOutRunScopeEvent: advisory locker not initialized; peers not notified",
			"run_scope_id", runScopeID.String())
		return
	}
	peers := peersForSpec(tplSpec)
	scopeID := runScopeID.String()

	for _, name := range peers {
		if err := withOptionalFanOutTx(ctx, persist, func(ctx context.Context, useTx persistence.Tx) error {
			if err := advLock.TakeLifecycleScopeLock(ctx,
				persistence.LifecycleIdempotencyScopeRunScope, scopeID, useTx); err != nil {
				return err
			}
			existing, err := persist.LifecycleIdempotency().Get(
				ctx, name, persistence.LifecycleIdempotencyScopeRunScope, scopeID, useTx)
			if err != nil {
				return err
			}
			if existing != nil && existing.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
				return nil
			}

			sub, ok := lifecycleSubs.Get(name)
			if !ok {
				return nil
			}

			req := lifecycle.OnRunScopeTerminalRequest{
				RunScopeID:     scopeID,
				TerminalReason: terminalReason,
				InstanceID:     instanceID.String(),
			}
			if err := sub.OnRunScopeTerminal(ctx, req); err != nil {
				warnFanOut(logger, "FanOutRunScopeEvent: peer delivery failed; idempotency row not advanced, peer will be retried on the next terminal fan-out for this scope",
					"peer", name, "run_scope_id", scopeID, "error", err)
				return nil
			}

			return persist.LifecycleIdempotency().Upsert(ctx,
				persistence.LifecycleIdempotencyRow{
					ClaimProducerName: name,
					ScopeKind:         persistence.LifecycleIdempotencyScopeRunScope,
					ScopeID:           scopeID,
					State:             persistence.LifecycleIdempotencyStateRunScopeTerminal,
				}, useTx,
			)
		}, tx); err != nil {
			warnFanOut(logger, "FanOutRunScopeEvent: peer fan-out transaction failed; peer will be retried on the next terminal fan-out for this scope",
				"peer", name, "run_scope_id", scopeID, "error", err)
			continue
		}
	}
}
