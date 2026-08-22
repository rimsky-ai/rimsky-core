// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
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
	return func(ctx context.Context, instanceID, runScopeID shared.UUID, terminalReason string) {
		var tplSpec node.TemplateSpec
		if err := persist.Transaction(ctx, func(ctx context.Context, useTx persistence.Tx) error {
			inst, err := persist.Instances().Get(ctx, instanceID, useTx)
			if err != nil {
				return err
			}
			if inst == nil {
				return errInstanceGone
			}
			tpl, err := persist.Templates().GetByHash(ctx, inst.TemplateHash, useTx)
			if err != nil {
				return err
			}
			if tpl == nil {
				return errTemplateGone
			}
			tplSpec = tpl.Spec
			return nil
		}); err != nil {
			slog.Warn("lifecycle_fanout.scope_lookup_failed",
				"instance_id", instanceID.String(),
				"run_scope_id", runScopeID.String(),
				"error", err,
				"consequence", "the instance or template row is gone, so no peer hears this run-scope terminal")
			return
		}
		FanOutRunScopeEvent(ctx, persist, advLock, lifecycleSubs, peersForSpec,
			tplSpec, runScopeID, instanceID, terminalReason, nil)
	}
}

var (
	errInstanceGone = errors.New("FrameRunScopeTerminalFanout: instance row is gone")
	errTemplateGone = errors.New("FrameRunScopeTerminalFanout: template row is gone")
)

func warnFanOut(logger shared.Logger, msg string, kv ...any) {
	if logger != nil {
		logger.Warn(msg, kv...)
		return
	}
	slog.Warn(msg, kv...)
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
) {
	if lifecycleSubs == nil || peersForSpec == nil {
		return
	}
	if advLock == nil {
		warnFanOut(logger, "lifecycle_fanout.advisory_locker_missing",
			"run_scope_id", runScopeID.String(),
			"consequence", "the fan-out serializes on an advisory lock it does not have, so no peer hears this run-scope terminal")
		return
	}
	peers := peersForSpec(tplSpec)
	scopeID := runScopeID.String()

	for _, name := range peers {
		if err := persist.Transaction(ctx, func(ctx context.Context, useTx persistence.Tx) error {
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
				warnFanOut(logger, "lifecycle_fanout.peer_delivery_failed",
					"peer", name,
					"run_scope_id", scopeID,
					"error", err,
					"consequence", "the idempotency row keeps its previous state, so the next terminal fan-out for this scope retries the peer")
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
		}); err != nil {
			warnFanOut(logger, "lifecycle_fanout.peer_transaction_failed",
				"peer", name,
				"run_scope_id", scopeID,
				"error", err,
				"consequence", "the next terminal fan-out for this scope retries the peer")
			continue
		}
	}
}
