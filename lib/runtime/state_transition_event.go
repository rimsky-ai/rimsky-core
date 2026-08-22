// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: transition-reason
// @concept: event-log
// @decision: event-log-kind-enum
func AppendStateTransitionEvent(
	ctx context.Context,
	persist persistence.Tables,
	nodeID, instanceID shared.UUID,
	from, to cascade.NodeState,
	reason cascade.TransitionReason,
	tx persistence.Tx,
) error {
	if persist == nil || persist.Events() == nil {
		return nil
	}
	return persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       events.KindStateTransition(),
		Payload: eventpayload.New(&genv1.StateTransitionPayload{
			From:   string(from),
			To:     string(to),
			Reason: reason.Kind,
		}),
	}, tx)
}
