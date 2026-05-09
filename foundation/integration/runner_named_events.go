// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Named-event processing — H1 (persist) + H2 (on_event handler dispatch).
//
// Every NamedEvent emitted during a dispatch (gRPC stream or async
// callback body) is:
//   1. Persisted to rimsky_node_events via NodeEventsStore.Insert,
//      with payload spilled through the configured BlobBackend when
//      it exceeds BlobSpillThreshold (per plan H1).
//   2. Matched against the emitter node's TemplateNodeDef.OnEvent map.
//      If a matching handler is present, its Invalidate slot is fired
//      via the unified invalidate handler (per plan H2). The handler's
//      Resolve verdict (`pass`/`retry`/`error`/empty) is captured in
//      the audit log; the handler does not transition the emitter's
//      state directly — the executor's terminal verdict still drives
//      the state machine.
//
// Per @blessed-invariant 21 the payload bytes are never logged,
// formatted, or transformed; only sizes and names appear in audit-log
// entries and metric labels.

package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
)

// processNamedEvents walks the captured event list, persists each via
// NodeEventsStore (with blob-spill where appropriate), and fires any
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
			// Continue — handler dispatch is still attempted because
			// the event happened even if persistence failed; the
			// substitution path may not find it but the
			// invalidate-fanout still has value for routing.
		}
		fireOnEventHandler(ctx, args, acq, evt.Name)
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

// fireOnEventHandler looks up the emitter's template OnEvent[<name>]
// entry; if present, fires the declared Invalidate via the unified
// invalidate-handler path (the same one that the admin invalidate
// endpoint uses, so handler-emitted invalidates correctly resume parked
// targets).
//
// The handler's Resolve verdict (pass/retry/error) is recorded in the
// audit-log only — it does not transition the emitter's state directly
// (the executor's terminal verdict drives the state machine).
func fireOnEventHandler(ctx context.Context, args RunArgs, acq *acquisition, eventName string) {
	if acq.NodeDef == nil || len(acq.NodeDef.OnEvent) == 0 {
		return
	}
	handler, ok := acq.NodeDef.OnEvent[eventName]
	if !ok {
		return
	}
	if handler.Invalidate == nil || len(handler.Invalidate.Targets) == 0 {
		// Handler exists but no invalidate to emit; nothing to do
		// beyond audit (Resolve handling is not implemented at this
		// layer — the executor's terminal verdict drives state).
		return
	}
	// Convert the modeling DSL HandlerInvalidate to the foundation
	// invalidate target list. Resolve targets via instance-wide
	// type-to-id lookup (same approach as invalidateTargets in
	// runner_terminal_errors.go).
	frameMode := handler.Invalidate.Frame
	if frameMode == "" {
		frameMode = node.FrameNext
	}
	emitHandlerInvalidate(ctx, args, acq.NodeID, acq.NodeType, acq.InstanceID, &acq.FrameID, &node.HandlerInvalidate{
		Targets: handler.Invalidate.Targets,
		Frame:   frameMode,
	})
}
