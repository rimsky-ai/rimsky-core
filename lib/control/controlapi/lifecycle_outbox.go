// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func lifecycleDeliveryDeps(deps AppDeps) runtime.LifecycleDeliveryDeps {
	return runtime.LifecycleDeliveryDeps{
		Persist:        deps.Persist,
		AdvisoryLocker: deps.AdvisoryLocker,
		Subscribers:    deps.LifecycleSubs,
		Logger:         deps.Logger,
	}
}

// @decision: lifecycle-drain-per-role
func kickLifecycleDrain(deps AppDeps) {
	if deps.LifecycleKick == nil {
		return
	}
	deps.LifecycleKick()
}

// @decision: lifecycle-fanout-after-commit
// @decision: lifecycle-drain-per-role
func deliverStagedLifecycleAfterCommit(
	ctx context.Context,
	deps AppDeps,
	scopeKind persistence.LifecycleScopeKind,
	scopeID string,
	kv ...any,
) {
	kickLifecycleDrain(deps)
	perServiceErr, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(deps), scopeKind, scopeID)
	logFanOutFailures(deps, "LIFECYCLE.FANOUTDELIVERY.FAILED", perServiceErr, err, kv...)
}

// @decision: lifecycle-fanout-after-commit
func stageInstanceCreated(
	ctx context.Context,
	deps AppDeps,
	templateHash string,
	spec node.TemplateSpec,
	resp createInstanceResponse,
	params map[string]any,
	bindings json.RawMessage,
	owner *shared.UUID,
	targetRoutingIdentity string,
	tx persistence.Tx,
) error {
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal instance params for %s: %w", resp.InstanceID, err)
	}
	instanceKey := ""
	if resp.InstanceKey != nil {
		instanceKey = *resp.InstanceKey
	}
	return lifecycle.StageInstanceEvent(ctx, deps.Persist.LifecycleOutbox(), lifecycle.EventInstanceCreated,
		templateHash, resp.InstanceID, spec, deps.LateBindServiceProxies,
		lifecycle.InstancePayload{
			InstanceKey:           instanceKey,
			Params:                paramsBytes,
			ServiceBindings:       bindings,
			OwnerAPIKeyID:         owner,
			TargetRoutingIdentity: targetRoutingIdentity,
		}, tx)
}
