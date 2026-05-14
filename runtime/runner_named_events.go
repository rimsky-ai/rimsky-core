// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Named-event processing — persists each emitted NamedEvent.
//
// Every NamedEvent emitted during a dispatch (gRPC stream or async
// callback body) is persisted to rimsky_node_events via
// NodeEventTable.Insert, with payload spilled through the configured
// BlobBackend when it exceeds BlobSpillThreshold.
//
// Post-2026-05-14 the per-event invalidate-emit handler retires: the
// receiver-side `subscribes: [{node, on: event, name}]` declaration
// replaces the substitution-path and the cascade-fire path uniformly.
// The cascade walk picks up the emission via the subscription-edge
// match in cascadeSubscribersStaleInTx.
//
// Per @blessed-invariant 21 the payload bytes are never logged,
// formatted, or transformed; only sizes and names appear in audit-log
// entries and metric labels.

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// processNamedEvents walks the captured event list, persists each via
// NodeEventTable (with blob-spill where appropriate), and fires any
// matching on_event handlers on the emitter node. Best-effort: failures
// to persist a single event are logged and the next event is processed;
// the terminal verdict is not blocked.
func processNamedEvents(ctx context.Context, args RunArgs, acq *acquisition, events []namedEventRecord) {
	if len(events) == 0 {
		return
	}
	for _, evt := range events {
		if err := persistOneNamedEvent(ctx, args, acq, evt); err != nil {
			args.Logger.Warn("processNamedEvents: persist failed",
				"node_id", acq.NodeID.String(),
				"event_name", evt.Name,
				"error", err.Error())
		}
		// Receiver-side `subscribes: [{node, on: event, name}]` handles
		// downstream cascade-fire via the subscription-edge match in
		// cascadeSubscribersStaleInTx; no per-emit handler dispatch here.
	}
}

// persistOneNamedEvent writes one row to rimsky_node_events. Spills the
// payload through args.Blob when above threshold; otherwise stores
// inline.
func persistOneNamedEvent(ctx context.Context, args RunArgs, acq *acquisition, evt namedEventRecord) error {
	var (
		inline        []byte
		handle        string
		handleBackend string
	)
	// If the record already carries a handle (e.g. from the
	// async-callback path that has already spilled), pass through.
	if evt.PayloadHandle != "" {
		handle = evt.PayloadHandle
		handleBackend = evt.PayloadHandleBackend
	} else if shouldSpillBlob(args, len(evt.PayloadInline)) {
		key := persistence.BlobKey{
			NodeID: acq.NodeID.String(),
			Hint:   "named_event:" + evt.Name,
		}
		h, err := args.Blob.Write(ctx, key, evt.PayloadInline)
		if err != nil {
			args.Logger.Warn("persistOneNamedEvent: blob spill failed; falling back to inline",
				"node_id", acq.NodeID.String(), "event_name", evt.Name,
				"error", err.Error())
			inline = evt.PayloadInline
		} else {
			handle = string(h)
			handleBackend = args.Blob.Name()
		}
	} else {
		inline = evt.PayloadInline
	}

	row := persistence.NodeEvent{
		InstanceID:           acq.InstanceID.String(),
		EmitterNodeID:        acq.NodeID.String(),
		EventName:            evt.Name,
		PayloadInline:        inline,
		PayloadHandle:        handle,
		PayloadHandleBackend: handleBackend,
		FrameID:              acq.FrameID.String(),
	}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := args.Persist.NodeEvents().Insert(ctx, row, tx)
		if err != nil {
			return fmt.Errorf("NodeEvents.Insert(%s/%s/%s): %w",
				acq.InstanceID, acq.NodeID, evt.Name, err)
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "named_event_emitted",
			Payload: map[string]any{
				"event_name":      evt.Name,
				"payload_bytes":   len(evt.PayloadInline),
				"spilled_to_blob": handle != "",
			},
		}, tx)
	}); err != nil {
		return err
	}
	// Increment the named-event counter only after a successful persist
	// so failed inserts don't inflate `rimsky_named_events_total`.
	metricsOf(args).IncNamedEvent(acq.Executor, evt.Name)
	return nil
}

// fireOnEventHandler retired by the 2026-05-14 subscription-cascade
// resolution. Receiver-side `subscribes: [{node, on: event, name}]`
// replaces the substitution path and the cascade-fire path uniformly.
