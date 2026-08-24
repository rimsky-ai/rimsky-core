// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"
	"sort"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"google.golang.org/protobuf/types/known/structpb"
)

// @concept: claim
// @concept: event-log
// @decision: event-log-kind-enum
func emitClaimAcquired(
	ctx context.Context, args RunArgs, acq acquisition, lk AcquiredLock, sp claimproducer.ClaimSpec, tx persistence.Tx,
) error {
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindClaimAcquired(),
		Payload: eventpayload.New(&genv1.ClaimAcquiredPayload{
			ClaimId:      lk.ClaimHandleID.String(),
			ProducerName: sp.ProducerName,
			Hold:         isAliasHeld(acq.HeldSubgraphs, acq.NodeType, sp.Alias),
		}),
	}, tx); err != nil {
		return fmt.Errorf("emitClaimAcquired: %w", err)
	}
	return nil
}

// @concept: claim-co-holdership
// @concept: event-log
// @decision: event-log-kind-enum
func emitClaimHeld(
	ctx context.Context, args RunArgs, acq *acquisition, lk AcquiredLock, sp claimproducer.ClaimSpec, tx persistence.Tx,
) error {
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindClaimHeld(),
		Payload: eventpayload.New(&genv1.ClaimHeldPayload{
			ClaimId:         lk.ClaimHandleID.String(),
			ProducerName:    sp.ProducerName,
			TerminalNodeIds: holdingSubgraphMembers(acq.HeldSubgraphs, acq.NodeType, sp.Alias),
		}),
	}, tx); err != nil {
		return fmt.Errorf("emitClaimHeld: %w", err)
	}
	return nil
}

// @concept: claim-handle
// @concept: event-log
// @decision: event-log-kind-enum
func emitClaimResolved(
	ctx context.Context, args RunArgs, acq *acquisition, claimHandleID shared.UUID, producerName, action string, tx persistence.Tx,
) error {
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindClaimResolved(),
		Payload: eventpayload.New(&genv1.ClaimResolvedPayload{
			Action:       action,
			ClaimId:      claimHandleID.String(),
			ProducerName: producerName,
		}),
	}, tx); err != nil {
		return fmt.Errorf("emitClaimResolved: %w", err)
	}
	return nil
}

// @concept: attribute
// @concept: event-log
// @decision: event-log-kind-enum
func emitAttributesCommitted(
	ctx context.Context, args RunArgs, acq *acquisition, changed bool, delta map[string]any, changeSummary string, tx persistence.Tx,
) error {
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindAttributesCommitted(),
		Payload: eventpayload.New(&genv1.AttributesCommittedPayload{
			Changed:       changed,
			UpdatedFields: sortedKeys(delta),
			ChangeSummary: changeSummary,
		}),
	}, tx); err != nil {
		return fmt.Errorf("emitAttributesCommitted: %w", err)
	}
	return nil
}

// @concept: event-log
// @decision: event-log-kind-enum
func emitNoOpCommit(
	ctx context.Context, args RunArgs, acq *acquisition, reason string, tx persistence.Tx,
) error {
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind:    events.KindNoOpCommit(),
		Payload: eventpayload.New(&genv1.NoOpCommitPayload{Reason: reason}),
	}, tx); err != nil {
		return fmt.Errorf("emitNoOpCommit: %w", err)
	}
	return nil
}

// @concept: event-log
// @concept: error-policy
// @decision: event-log-kind-enum
func emitErrorPolicyApplied(
	ctx context.Context, args RunArgs, acq *acquisition, errorClass, actionTaken string, delayMs int64, details map[string]any, tx persistence.Tx,
) error {
	payload := &genv1.ErrorPayload{
		ErrorClass:  errorClass,
		ActionTaken: actionTaken,
		DelayMs:     delayMs,
	}
	payload.Details = detailsAsStruct(args, acq, "emitErrorPolicyApplied", errorClass, details)
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind:    events.KindError(),
		Payload: eventpayload.New(payload),
	}, tx); err != nil {
		return fmt.Errorf("emitErrorPolicyApplied: %w", err)
	}
	return nil
}

// @concept: event-log
// @concept: attribute
// @decision: event-log-kind-enum
func emitWorkRejected(
	ctx context.Context, args RunArgs, acq *acquisition, reason string, details map[string]any,
) {
	if args.Persist == nil || args.Persist.Events() == nil {
		return
	}
	payload := &genv1.WorkRejectedPayload{Reason: reason}
	payload.Errors = detailsAsStruct(args, acq, "emitWorkRejected", reason, details)
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind:    events.KindWorkRejected(),
			Payload: eventpayload.New(payload),
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("RUNNER.WORKREJECTEDEVENT.APPENDFAILED", "site", "emitWorkRejected", "detail", "the rejection record is lost",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.NodeRunID.String(),
			"reason", reason,
			"error", err.Error())
	}
}

// @concept: event-log
func detailsAsStruct(args RunArgs, acq *acquisition, site, reason string, details map[string]any) *structpb.Struct {
	out, err := structpb.NewStruct(jsonSafeMap(details))
	if err != nil {
		if args.Logger != nil {
			args.Logger.Warn("RUNNER.EVENTDETAILS.CONVERSIONFAILED", "site", site,
				"detail", "the event records the transition without the details",
				"node_id", acq.NodeID.String(),
				"dispatch_id", acq.NodeRunID.String(),
				"reason", reason,
				"error", err.Error())
		}
		return nil
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func jsonSafeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string, bool, float64, int, int64, nil:
			out[k] = t
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out
}

func holdingSubgraphMembers(subgraphs []node.HoldingSubgraph, acquirerType, alias string) []string {
	for _, h := range subgraphs {
		if h.AcquirerType == acquirerType && h.Alias == alias {
			return h.Members
		}
	}
	return nil
}
