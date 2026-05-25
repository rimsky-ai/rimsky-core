// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package audit owns the persistence-side audit-emit pathway for
// signals. Every Signal a producer hands to EmitSignal becomes one
// rimsky_events row with kind = string(sig.Type) and payload =
// sig.Payload. Audit emission is unconditional — it does not consult
// subscribers; the cascade walker is the other branch of the signal
// pathway and runs independently.
//
// Split out from the parent `signal` package so that package can be
// imported by foundation/spec (which is depended on by
// foundation/persistence) without creating an import cycle. Per
// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design` Pass 3.
package audit

import (
	"context"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	shared "github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/signal"
)

// EmitSignal writes one rimsky_events row per emitted signal. The
// sole audit-emit pathway for signal-bearing transitions. The
// transaction is the caller's responsibility — EmitSignal threads
// the supplied tx through to persistence.EventTable.Append, so the
// signal write commits or rolls back atomically with the surrounding
// state-mutation tx.
//
// The instanceID and nodeID arguments are required (callers pass
// them through from the acquisition / dispatch context); a zero
// occurredAt is permitted but discouraged — pass args.Clock.Now()
// from the runtime call site so the audit row has the same
// timestamp as the surrounding state-mutation work.
//
//	@concept: signal
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
	in := persistence.EventAppendInput{
		InstanceID: &instanceID,
		Kind:       string(sig.Type),
		Payload:    payload,
	}
	// NodeID is optional — message signals arrive at the instance
	// level before any per-node binding has happened, so callers can
	// pass a zero UUID to leave the field NULL.
	if nodeID != (shared.UUID{}) {
		in.NodeID = &nodeID
	}
	if !occurredAt.IsZero() {
		in.OccurredAt = &occurredAt
	}
	return events.Append(ctx, in, tx)
}
