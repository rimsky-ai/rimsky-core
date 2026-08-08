// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

//	@concept: child-execution
//	@concept: sub-graph
//	@concept: run-scope
//	@concept: claim-tree

package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type PartitionDescriptor struct {
	PartitionKey     string
	SubClaimHandleID shared.UUID
}

type ChildRunSpec struct {
	NodeID                 shared.UUID
	Executor               string
	RequiredClaimProducers []string
}

type ChildExecutionInput struct {
	ParentNodeRunID   shared.UUID
	ParentRunScopeID  shared.UUID
	InstanceID        shared.UUID
	FrameID           shared.UUID
	ChildGraphName    string
	AggregationPolicy spec.AggregationPolicy
	// @decision: entry-absorption-flag
	EntryAbsorbed bool
	Partitions    []PartitionDescriptor
	Children      []ChildRunSpec
}

type DispatchedChild struct {
	NodeRunID    shared.UUID
	RunScopeID   shared.UUID
	NodeID       shared.UUID
	PartitionKey string
}

// @concept: child-execution
// @concept: run-scope
// @decision: fan-out-and-delegation-are-distinct-mechanisms
// @decision: child-execution-naming
// @decision: subclaims-as-input
func DispatchChildren(
	ctx context.Context, args RunArgs, in ChildExecutionInput, tx persistence.Tx,
) ([]DispatchedChild, error) {
	if len(in.Partitions) == 0 || len(in.Children) == 0 {
		return nil, nil
	}
	parentScope, err := args.Persist.RunScopes().GetByID(ctx, in.ParentRunScopeID, tx)
	if err != nil {
		return nil, fmt.Errorf("DispatchChildren: load parent run scope: %w", err)
	}
	if parentScope == nil {
		return nil, fmt.Errorf("DispatchChildren: parent run scope %s not found", in.ParentRunScopeID)
	}
	if in.EntryAbsorbed {
		if err := rejectDelegateRecursionInChain(ctx, args.Persist.RunScopes(), parentScope.ID, in.ChildGraphName, tx); err != nil {
			return nil, fmt.Errorf("DispatchChildren: %w", err)
		}
	}
	out := make([]DispatchedChild, 0, len(in.Partitions)*len(in.Children))
	for _, p := range in.Partitions {
		if p.SubClaimHandleID != (shared.UUID{}) && len(in.Children) != 1 {
			return nil, fmt.Errorf(
				"DispatchChildren: partition %q carries a sub-claim handle but %d child specs; a sub-claim binds to exactly one run",
				p.PartitionKey, len(in.Children))
		}
		var childScopeID shared.UUID
		existing, err := args.Persist.RunScopes().GetFanoutPartition(ctx, in.ParentNodeRunID, p.PartitionKey, tx)
		if err != nil {
			return nil, fmt.Errorf("DispatchChildren: lookup partition %q: %w", p.PartitionKey, err)
		}
		if existing != nil {
			childScopeID = existing.ID
		} else {
			childScopeID = shared.UUID(uuid.New())
			parentRunIDCopy := in.ParentNodeRunID
			parentScopeIDCopy := parentScope.ID
			if err := args.Persist.RunScopes().Create(ctx, persistence.RunScopeRow{
				ID:               childScopeID,
				ParentRunScopeID: &parentScopeIDCopy,
				ParentNodeRunID:  &parentRunIDCopy,
				GraphName:        in.ChildGraphName,
				PartitionKey:     p.PartitionKey,
				InstanceID:       in.InstanceID,
			}, tx); err != nil {
				return nil, fmt.Errorf("DispatchChildren: create partition %q: %w", p.PartitionKey, err)
			}
		}
		for _, c := range in.Children {
			runID, err := CreateChildNodeRun(
				ctx, args.Persist.NodeRunTree(), args.Queue, args.Clock, c.NodeID, in.FrameID, childScopeID, c.Executor, c.RequiredClaimProducers, in.AggregationPolicy, tx)
			if err != nil {
				return nil, fmt.Errorf("DispatchChildren: child run (partition %q, node %s): %w",
					p.PartitionKey, c.NodeID, err)
			}
			if p.SubClaimHandleID != (shared.UUID{}) {
				if args.Persist.ClaimHandles() == nil {
					return nil, fmt.Errorf("DispatchChildren: partition %q carries sub-claim %s but no claim-handle table is wired",
						p.PartitionKey, p.SubClaimHandleID)
				}
				if err := args.Persist.ClaimHandles().UpdateNodeRunID(ctx, p.SubClaimHandleID, runID, args.SupervisorID, tx); err != nil {
					return nil, fmt.Errorf("DispatchChildren: link sub-claim %s to child run %q: %w",
						p.SubClaimHandleID, p.PartitionKey, err)
				}
			}
			out = append(out, DispatchedChild{
				NodeRunID:    runID,
				RunScopeID:   childScopeID,
				NodeID:       c.NodeID,
				PartitionKey: p.PartitionKey,
			})
		}
	}
	return out, nil
}

// @concept: sub-graph
// @concept: run-scope
func rejectDelegateRecursionInChain(
	ctx context.Context, scopes persistence.RunScopeTable, parentRunScopeID shared.UUID, childGraphName string, tx persistence.Tx,
) error {
	chain, err := scopes.ListParentChain(ctx, parentRunScopeID, tx)
	if err != nil {
		return fmt.Errorf("rejectDelegateRecursionInChain: walk parent chain of %s: %w", parentRunScopeID, err)
	}
	for _, s := range chain {
		if s.GraphName == childGraphName {
			return fmt.Errorf(
				"rejectDelegateRecursionInChain: sub-graph %q is already open in the run-scope ancestor chain (scope %s); recursive delegation is rejected at dispatch",
				childGraphName, s.ID)
		}
	}
	return nil
}

type DelegateSettlementInput struct {
	ExitNodeRunID shared.UUID
	ExitNodeID    shared.UUID
	ExitNodeAlias string
	InstanceID    shared.UUID
	Writeback     json.RawMessage
}

type FanoutChildSettlementInput struct {
	ParentClaimHandleID   shared.UUID
	ChildClaimHandleID    shared.UUID
	ChildOutcome          TerminalOutcome
	ChildProducerMetadata []byte
}

// @concept: child-execution
// @concept: run-scope
// @concept: delegation
func SettleFromDelegate(
	ctx context.Context, args RunArgs, in DelegateSettlementInput, tx persistence.Tx,
) (postCommitFn, error) {
	if args.Persist == nil {
		return nil, fmt.Errorf("SettleFromDelegate: Persist is required")
	}
	rt := args.Persist.NodeRunTree()
	if rt == nil {
		return nil, fmt.Errorf("SettleFromDelegate: NodeRunTree is required")
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return nil, fmt.Errorf("SettleFromDelegate: RunScopes is required")
	}
	exit, err := rt.GetByID(ctx, in.ExitNodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromDelegate: load exit run %s: %w", in.ExitNodeRunID, err)
	}
	if exit == nil {
		return nil, fmt.Errorf("SettleFromDelegate: run %s not found", in.ExitNodeRunID)
	}
	exitScope, err := scopes.GetByID(ctx, exit.RunScopeID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromDelegate: load exit run scope %s: %w", exit.RunScopeID, err)
	}
	if exitScope == nil || exitScope.ParentNodeRunID == nil {
		return nil, fmt.Errorf("SettleFromDelegate: run %s has no parent; not a sub-graph exit", in.ExitNodeRunID)
	}
	var asMap map[string]any
	if len(in.Writeback) > 0 {
		if err := json.Unmarshal(in.Writeback, &asMap); err != nil {
			return nil, fmt.Errorf("SettleFromDelegate: exit writeback bytes not JSON-decodable: %w", err)
		}
	}
	parentNodeRunID := *exitScope.ParentNodeRunID
	parent, err := rt.GetByID(ctx, parentNodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromDelegate: load parent run %s: %w", parentNodeRunID, err)
	}
	if parent == nil {
		return nil, fmt.Errorf("SettleFromDelegate: parent run %s not found", parentNodeRunID)
	}
	if args.Logger != nil {
		args.Logger.Info("subgraph: carry exit writeback to parent run",
			"exit_run_id", in.ExitNodeRunID.String(),
			"parent_run_id", parentNodeRunID.String(),
			"parent_node_id", parent.NodeID.String(),
			"writeback_field_count", len(asMap))
	}
	if args.Persist.NodeAttributes() == nil {
		return nil, fmt.Errorf("SettleFromDelegate: NodeAttributes is required")
	}
	if len(in.Writeback) > 0 {
		if err := args.Persist.NodeAttributes().Upsert(
			ctx, parent.NodeRunID, parent.NodeID, asMap, tx,
		); err != nil {
			return nil, fmt.Errorf("SettleFromDelegate: upsert parent attributes: %w", err)
		}
	}
	// @concept: run-scope
	if err := scopes.Close(ctx, exit.RunScopeID, tx); err != nil {
		return nil, fmt.Errorf("SettleFromDelegate: close sub-graph run scope %s: %w", exit.RunScopeID, err)
	}
	var fanoutPC postCommitFn
	if instTbl, tplTbl := args.Persist.Instances(), args.Persist.Templates(); instTbl != nil && tplTbl != nil {
		inst, err := instTbl.Get(ctx, exitScope.InstanceID, tx)
		if err != nil && args.Logger != nil {
			args.Logger.Warn("SettleFromDelegate: instance lookup failed; subgraph_exit fan-out skipped",
				"instance_id", exitScope.InstanceID.String(), "error", err.Error())
		}
		if err == nil && inst != nil {
			tpl, err := tplTbl.GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil && args.Logger != nil {
				args.Logger.Warn("SettleFromDelegate: template lookup failed; subgraph_exit fan-out skipped",
					"template_hash", inst.TemplateHash, "error", err.Error())
			}
			if err == nil && tpl != nil {
				fanoutPC = fanOutRunScopeEventPostCommit(args, tpl.Spec, exit.RunScopeID,
					exitScope.InstanceID, "subgraph_exit")
			}
		}
	}
	// @concept: claim-handle
	// @concept: claim-tree
	callerClaimsPC, err := resolveDelegateCallerClaimsInTx(ctx, args, *parent, in.InstanceID, OutcomeCommit, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromDelegate: resolve calling node's own claims: %w", err)
	}
	// @concept: cascade
	// @decision: cascade-inside-settlement
	if args.Persist.Nodes() == nil {
		return nil, fmt.Errorf("SettleFromDelegate: Nodes table is required for the parent-settlement cascade bridge")
	}
	callingNodeRow, err := args.Persist.Nodes().Get(ctx, parent.NodeID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromDelegate: load calling node: %w", err)
	}
	if callingNodeRow != nil {
		parentFrameID := parent.FrameID
		exitBridgeSig := signalpkg.BuildTerminalSuccessSignal(true, asMap, "subgraph_exit_carry", nil)
		if err := cascadeSubscribersStaleInTx(ctx, args, parent.NodeID, callingNodeRow.NodeType, parent.NodeRunID, in.InstanceID, parentFrameID, exitBridgeSig, tx); err != nil {
			return nil, fmt.Errorf("SettleFromDelegate: cascade subscribers of calling node: %w", err)
		}
		if err := emitAttributeChangesForRunInTx(ctx, args, parent.NodeID, callingNodeRow.NodeType, parent.NodeRunID, in.InstanceID, parentFrameID, nil, nil, tx); err != nil {
			return nil, fmt.Errorf("SettleFromDelegate: emit parent attribute changes: %w", err)
		}
		if err := drainWaitSetOnSettled(ctx, args, parentFrameID, parent.NodeRunID, tx); err != nil {
			return nil, fmt.Errorf("SettleFromDelegate: drain wait-set for calling node: %w", err)
		}
	}
	if args.Persist.Events() == nil {
		return nil, fmt.Errorf("SettleFromDelegate: Events table is required for the exit-carry forensics record")
	}
	nodeID := in.ExitNodeID
	instanceID := in.InstanceID
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       events.KindSubgraphExitCarry(),
		Payload: eventpayload.New(&genv1.SubgraphExitCarryPayload{
			ParentRunId:   parentNodeRunID.String(),
			ExitRunId:     in.ExitNodeRunID.String(),
			ExitNodeAlias: in.ExitNodeAlias,
			Outcome:       "fresh",
		}),
	}, tx); err != nil {
		return nil, err
	}
	return chainPostCommit(callerClaimsPC, fanoutPC), nil
}

func fanOutRunScopeEventPostCommit(
	args RunArgs, tplSpec node.TemplateSpec, runScopeID, instanceID shared.UUID, terminalReason string,
) postCommitFn {
	return func(ctx context.Context) {
		FanOutRunScopeEvent(ctx, args.Persist, args.AdvisoryLocker, args.LifecycleSubs,
			args.LifecyclePeersForSpec, tplSpec, runScopeID, instanceID, terminalReason, args.Logger, nil)
	}
}

// @concept: claim-handle
// @concept: claim-tree
// @decision: fan-out-and-delegation-are-distinct-mechanisms
func resolveDelegateCallerClaimsInTx(
	ctx context.Context, args RunArgs, parent persistence.NodeRunTreeRow, instanceID shared.UUID, outcome TerminalOutcome, tx persistence.Tx,
) (postCommitFn, error) {
	if args.ClaimHandles == nil {
		return nil, nil
	}
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, parent.NodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("resolveDelegateCallerClaimsInTx: ListByNodeRun: %w", err)
	}
	var post postCommitFn
	for i := range rows {
		row := rows[i]
		if row.IsHeld || row.State != spec.ClaimHandleStateActive {
			continue
		}
		if row.LockKind == persistence.LockKindNamed {
			if err := args.ClaimHandles.Delete(ctx, row.ID, args.SupervisorID, tx); err != nil {
				return nil, fmt.Errorf("resolveDelegateCallerClaimsInTx: named Delete %s: %w", row.ID, err)
			}
			continue
		}
		producerName := ""
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		producer, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, producerName, "", tx)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolveDelegateCallerClaimsInTx: resolving producer %q for claim %s: %w", producerName, row.ID, resolveErr)
		}
		if !ok {
			return nil, fmt.Errorf("resolveDelegateCallerClaimsInTx: unknown producer %q for claim %s", producerName, row.ID)
		}
		hint := ClaimLineageHint{
			InstanceID:   instanceID,
			FrameID:      parent.FrameID,
			NodeRunID:    parent.NodeRunID,
			NodeID:       parent.NodeID,
			ProducerName: producerName,
			VersionID:    row.VersionID,
		}
		pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
			ClaimHandleID:       row.ID,
			SupervisorID:        args.SupervisorID,
			Source:              ActiveTerminal,
			Outcome:             outcome,
			Producer:            producer,
			Scope:               []byte(row.ClaimScopeData),
			Address:             []byte(row.Address),
			LeaseToken:          row.ProducerLeaseToken,
			Lifetime:            row.Lifetime,
			CandidateHandle:     row.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: row.ParentClaimHandleID,
		}, tx)
		if err != nil {
			return nil, fmt.Errorf("resolveDelegateCallerClaimsInTx: resolve claim %s: %w", row.ID, err)
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}

// @concept: child-execution
// @concept: fan-out
// @concept: claim-tree
func SettleFromFanoutChild(
	ctx context.Context, args RunArgs, in FanoutChildSettlementInput, tx persistence.Tx,
) (postCommitFn, error) {
	parent, err := args.ClaimHandles.LockForUpdate(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, nil
	}
	if err := recordChildCommitMetadata(ctx, args, in, parent, tx); err != nil {
		return nil, fmt.Errorf("SettleFromFanoutChild: record child producer_metadata: %w", err)
	}
	if parent.HolderSupervisorID == nil || parent.State != spec.ClaimHandleStateActive {
		return nil, nil
	}
	outcomeKey := "commit"
	if in.ChildOutcome.IsAbandon() {
		outcomeKey = "abandon"
	}
	if err := args.ClaimHandles.BumpChildOutcomeCount(ctx, in.ParentClaimHandleID, *parent.HolderSupervisorID, outcomeKey, 1, tx); err != nil {
		return nil, fmt.Errorf("SettleFromFanoutChild: BumpChildOutcomeCount: %w", err)
	}
	var post postCommitFn
	// @concept: cancel-siblings
	if in.ChildOutcome.IsAbandon() {
		pc, err := cancelInFlightSiblings(ctx, args, in.ParentClaimHandleID, in.ChildClaimHandleID, tx)
		if err != nil {
			return nil, fmt.Errorf("SettleFromFanoutChild: cancelInFlightSiblings: %w", err)
		}
		post = chainPostCommit(post, pc)
	}
	{
		refreshed, err := args.ClaimHandles.Get(ctx, in.ParentClaimHandleID, tx)
		if err != nil {
			return nil, fmt.Errorf("SettleFromFanoutChild: re-read parent: %w", err)
		}
		if refreshed == nil || refreshed.HolderSupervisorID == nil || refreshed.State != spec.ClaimHandleStateActive {
			return post, nil
		}
		parent = refreshed
	}
	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromFanoutChild: ListByClaimHandleID: %w", err)
	}
	for _, h := range holders {
		if h.State == persistence.ClaimHolderStateActive {
			return post, nil
		}
	}
	children, err := args.ClaimHandles.ListChildClaimHandles(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromFanoutChild: ListChildClaimHandles: %w", err)
	}
	for _, c := range children {
		if c.State == spec.ClaimHandleStateActive {
			return post, nil
		}
	}
	// @concept: child-execution
	// @concept: run-scope
	if scopes := args.Persist.RunScopes(); scopes != nil {
		for _, c := range children {
			if c.NodeRunID == nil {
				continue
			}
			childRun, err := args.Persist.NodeRunTree().GetByID(ctx, *c.NodeRunID, tx)
			if err != nil {
				return nil, fmt.Errorf("SettleFromFanoutChild: load child run %s: %w", c.NodeRunID, err)
			}
			if childRun == nil {
				continue
			}
			childScope, err := scopes.GetByID(ctx, childRun.RunScopeID, tx)
			if err != nil {
				return nil, fmt.Errorf("SettleFromFanoutChild: load child run scope %s: %w", childRun.RunScopeID, err)
			}
			if childScope == nil || childScope.PartitionKey == "" {
				continue
			}
			if err := scopes.Close(ctx, childRun.RunScopeID, tx); err != nil {
				return nil, fmt.Errorf("SettleFromFanoutChild: close partition scope %s: %w", childRun.RunScopeID, err)
			}
			if instTbl, tplTbl := args.Persist.Instances(), args.Persist.Templates(); instTbl != nil && tplTbl != nil {
				inst, err := instTbl.Get(ctx, childScope.InstanceID, tx)
				if err != nil && args.Logger != nil {
					args.Logger.Warn("SettleFromFanoutChild: instance lookup failed; fanout_partition_terminal fan-out skipped",
						"instance_id", childScope.InstanceID.String(), "error", err.Error())
				}
				if err == nil && inst != nil {
					tpl, err := tplTbl.GetByHash(ctx, inst.TemplateHash, tx)
					if err != nil && args.Logger != nil {
						args.Logger.Warn("SettleFromFanoutChild: template lookup failed; fanout_partition_terminal fan-out skipped",
							"template_hash", inst.TemplateHash, "error", err.Error())
					}
					if err == nil && tpl != nil {
						post = chainPostCommit(post, fanOutRunScopeEventPostCommit(args, tpl.Spec,
							childRun.RunScopeID, childScope.InstanceID, "fanout_partition_terminal"))
					}
				}
			}
		}
	}
	outcome := aggregateParentOutcome(parent, in.ChildOutcome)
	producerName := ""
	if parent.ProducerName != nil {
		producerName = *parent.ProducerName
	}
	instID, err := acquirerInstanceID(ctx, args, parent.HolderNodeID, tx)
	if err != nil {
		return nil, fmt.Errorf("SettleFromFanoutChild: %w", err)
	}
	producer, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, producerName, instID.String(), tx)
	if resolveErr != nil {
		return nil, fmt.Errorf("SettleFromFanoutChild: resolving producer %q: %w", producerName, resolveErr)
	}
	if !ok {
		return nil, fmt.Errorf("SettleFromFanoutChild: unknown producer %q", producerName)
	}
	parentHint := ClaimLineageHint{
		ProducerName: producerName,
		VersionID:    parent.VersionID,
		NodeID:       parent.HolderNodeID,
	}
	if parent.FrameID != nil {
		parentHint.FrameID = *parent.FrameID
	}
	if parent.NodeRunID != nil {
		parentHint.NodeRunID = *parent.NodeRunID
	}
	parentHint.InstanceID = instID
	if *parent.HolderSupervisorID != args.SupervisorID {
		if err := args.ClaimHandles.ReassignHolderSupervisor(
			ctx, in.ParentClaimHandleID, *parent.HolderSupervisorID, args.SupervisorID, tx,
		); err != nil {
			return nil, fmt.Errorf("SettleFromFanoutChild: settlement takeover of parent %s (holder %s → %s): %w",
				in.ParentClaimHandleID, *parent.HolderSupervisorID, args.SupervisorID, err)
		}
	}
	pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
		ClaimHandleID:       in.ParentClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(parent.ClaimScopeData),
		Address:             []byte(parent.Address),
		LeaseToken:          parent.ProducerLeaseToken,
		Lifetime:            parent.Lifetime,
		CandidateHandle:     parent.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         parentHint,
		ParentClaimHandleID: parent.ParentClaimHandleID,
	}, tx)
	if err != nil {
		return nil, err
	}
	post = chainPostCommit(post, pc)
	// @concept: fan-out
	if args.FanOutSemaphores != nil && parent.NodeRunID != nil {
		args.FanOutSemaphores.Drop(*parent.NodeRunID)
	}
	return post, nil
}

func recordChildCommitMetadata(
	ctx context.Context, args RunArgs, in FanoutChildSettlementInput, parent *persistence.ClaimHandleRow, tx persistence.Tx,
) error {
	if in.ChildOutcome != OutcomeCommit || len(in.ChildProducerMetadata) == 0 {
		return nil
	}
	if parent.NodeRunID == nil {
		return nil
	}
	attrs := args.Persist.NodeAttributes()
	if attrs == nil {
		return fmt.Errorf("NodeAttributes is required")
	}
	key, err := childPartitionWritebackKey(ctx, args, in.ChildClaimHandleID, tx)
	if err != nil {
		return err
	}
	existing, err := attrs.GetByRun(ctx, *parent.NodeRunID, tx)
	if err != nil {
		return fmt.Errorf("load parent writeback row: %w", err)
	}
	merged := map[string]any{}
	if existing != nil && existing.Data != nil {
		merged = existing.Data
	}
	meta, _ := merged["producer_metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta[key] = base64.StdEncoding.EncodeToString(in.ChildProducerMetadata)
	merged["producer_metadata"] = meta
	if err := attrs.Upsert(ctx, *parent.NodeRunID, parent.HolderNodeID, merged, tx); err != nil {
		return fmt.Errorf("upsert parent writeback row: %w", err)
	}
	return nil
}

func childPartitionWritebackKey(
	ctx context.Context, args RunArgs, childID shared.UUID, tx persistence.Tx,
) (string, error) {
	fallback := childID.String()
	child, err := args.ClaimHandles.Get(ctx, childID, tx)
	if err != nil {
		return "", fmt.Errorf("load child claim handle %s: %w", childID, err)
	}
	if child == nil || child.NodeRunID == nil {
		return fallback, nil
	}
	childRun, err := args.Persist.NodeRunTree().GetByID(ctx, *child.NodeRunID, tx)
	if err != nil {
		return "", fmt.Errorf("load child run %s: %w", child.NodeRunID, err)
	}
	if childRun == nil {
		return fallback, nil
	}
	scope, err := args.Persist.RunScopes().GetByID(ctx, childRun.RunScopeID, tx)
	if err != nil {
		return "", fmt.Errorf("load child run scope %s: %w", childRun.RunScopeID, err)
	}
	if scope == nil || scope.PartitionKey == "" {
		return fallback, nil
	}
	return scope.PartitionKey, nil
}

func aggregateParentOutcome(parent *persistence.ClaimHandleRow, seedOutcome TerminalOutcome) TerminalOutcome {
	if parent == nil {
		return seedOutcome
	}
	if parent.ExpectedChildrenCount == 0 {
		return seedOutcome
	}
	policy, err := persistence.UnmarshalAggregationPolicy(parent.AggregationPolicy)
	if err != nil {
		policy = spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	}
	committed := parent.CommittedChildrenCount
	abandoned := parent.AbandonedChildrenCount
	switch policy.Kind {
	case spec.AggregationKindStrict:
		if abandoned > 0 {
			return OutcomeAbandon
		}
		return OutcomeCommit
	case spec.AggregationKindThreshold:
		max := policy.MaxFailures
		if max <= 0 {
			max = 1
		}
		if abandoned >= max {
			return OutcomeAbandon
		}
		return OutcomeCommit
	case spec.AggregationKindBestEffort, spec.AggregationKindFirst:
		if committed > 0 {
			return OutcomeCommit
		}
		return OutcomeAbandon
	default:
		if abandoned > 0 {
			return OutcomeAbandon
		}
		return OutcomeCommit
	}
}
