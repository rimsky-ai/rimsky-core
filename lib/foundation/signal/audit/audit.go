// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package audit

import (
	"context"
	"time"

	eventskinds "github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	shared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// @concept: signal
func EmitSignal(
	ctx context.Context,
	events persistence.EventTable,
	instanceID, nodeID shared.UUID,
	sig signal.Signal,
	occurredAt time.Time,
	tx persistence.Tx,
) error {
	payload := sig.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	// @decision: event-log-kind-enum — signal-class kinds carry the
	// canonical type-path as the event-log discriminator. Taxonomy
	// validation has happened at the signal emit site via
	// signal.ValidateTypePath; here we wrap the path opaquely.
	in := persistence.EventAppendInput{
		InstanceID: &instanceID,
		Kind:       eventskinds.SignalKind(string(sig.Type)),
		Payload:    payload,
	}
	if nodeID != (shared.UUID{}) {
		in.NodeID = &nodeID
	}
	if !occurredAt.IsZero() {
		in.OccurredAt = &occurredAt
	}
	return events.Append(ctx, in, tx)
}
