// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	ParentRunID       shared.UUID
	ParentRunScopeID  shared.UUID
	InstanceID        shared.UUID
	FrameID           shared.UUID
	ChildGraphName    string
	AggregationPolicy spec.AggregationPolicy
	EntryAbsorbed     bool
	Partitions        []PartitionDescriptor
	Children          []ChildRunSpec
}

type DispatchedChild struct {
	RunID        shared.UUID
	RunScopeID   shared.UUID
	NodeID       shared.UUID
	PartitionKey string
}

// @concept: child-execution
// @concept: run-scope
// @decision: fan-out-and-delegation-are-distinct-mechanisms
func DispatchChildren(
	ctx context.Context, args RunArgs, tx persistence.Tx, in ChildExecutionInput,
) ([]DispatchedChild, error) {
	if len(in.Partitions) == 0 || len(in.Children) == 0 {
		return nil, nil
	}
	parentScope, err := args.Persist.RunScopes().GetByID(ctx, tx, in.ParentRunScopeID)
	if err != nil {
		return nil, fmt.Errorf("DispatchChildren: load parent run scope: %w", err)
	}
	if parentScope == nil {
		return nil, fmt.Errorf("DispatchChildren: parent run scope %s not found", in.ParentRunScopeID)
	}
	out := make([]DispatchedChild, 0, len(in.Partitions)*len(in.Children))
	for _, p := range in.Partitions {
		if p.SubClaimHandleID != (shared.UUID{}) && len(in.Children) != 1 {
			return nil, fmt.Errorf(
				"DispatchChildren: partition %q carries a sub-claim handle but %d child specs; a sub-claim binds to exactly one run",
				p.PartitionKey, len(in.Children))
		}
		var childScopeID shared.UUID
		existing, err := args.Persist.RunScopes().GetFanoutPartition(ctx, tx, in.ParentRunID, p.PartitionKey)
		if err != nil {
			return nil, fmt.Errorf("DispatchChildren: lookup partition %q: %w", p.PartitionKey, err)
		}
		if existing != nil {
			childScopeID = existing.ID
		} else {
			childScopeID = shared.UUID(uuid.New())
			parentRunIDCopy := in.ParentRunID
			parentScopeIDCopy := parentScope.ID
			if err := args.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
				ID:               childScopeID,
				ParentRunScopeID: &parentScopeIDCopy,
				ParentRunID:      &parentRunIDCopy,
				GraphName:        in.ChildGraphName,
				PartitionKey:     p.PartitionKey,
				InstanceID:       in.InstanceID,
			}); err != nil {
				return nil, fmt.Errorf("DispatchChildren: create partition %q: %w", p.PartitionKey, err)
			}
		}
		for _, c := range in.Children {
			runID, err := CreateChildRun(
				ctx, tx, args.Persist.RunTree(), args.Queue,
				c.NodeID, in.FrameID, childScopeID,
				c.Executor, c.RequiredClaimProducers, in.AggregationPolicy)
			if err != nil {
				return nil, fmt.Errorf("DispatchChildren: child run (partition %q, node %s): %w",
					p.PartitionKey, c.NodeID, err)
			}
			if p.SubClaimHandleID != (shared.UUID{}) {
				if args.Persist.ClaimHandles() == nil {
					return nil, fmt.Errorf("DispatchChildren: partition %q carries sub-claim %s but no claim-handle table is wired",
						p.PartitionKey, p.SubClaimHandleID)
				}
				if err := args.Persist.ClaimHandles().UpdateNodeRunID(ctx, p.SubClaimHandleID, runID, tx); err != nil {
					return nil, fmt.Errorf("DispatchChildren: link sub-claim %s to child run %q: %w",
						p.SubClaimHandleID, p.PartitionKey, err)
				}
			}
			out = append(out, DispatchedChild{
				RunID:        runID,
				RunScopeID:   childScopeID,
				NodeID:       c.NodeID,
				PartitionKey: p.PartitionKey,
			})
		}
	}
	return out, nil
}

type DelegateSettlementInput struct {
	ExitRunID     shared.UUID
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
	ctx context.Context, args RunArgs, tx persistence.Tx, in DelegateSettlementInput,
) error {
	if args.Persist == nil {
		return fmt.Errorf("SettleFromDelegate: Persist is required")
	}
	rt := args.Persist.RunTree()
	if rt == nil {
		return fmt.Errorf("SettleFromDelegate: RunTree is required")
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return fmt.Errorf("SettleFromDelegate: RunScopes is required")
	}
	exit, err := rt.GetByID(ctx, tx, in.ExitRunID)
	if err != nil {
		return fmt.Errorf("SettleFromDelegate: load exit run %s: %w", in.ExitRunID, err)
	}
	if exit == nil {
		return fmt.Errorf("SettleFromDelegate: run %s not found", in.ExitRunID)
	}
	exitScope, err := scopes.GetByID(ctx, tx, exit.RunScopeID)
	if err != nil {
		return fmt.Errorf("SettleFromDelegate: load exit run scope %s: %w", exit.RunScopeID, err)
	}
	if exitScope == nil || exitScope.ParentRunID == nil {
		return fmt.Errorf("SettleFromDelegate: run %s has no parent; not a sub-graph exit", in.ExitRunID)
	}
	var asMap map[string]any
	if len(in.Writeback) > 0 {
		if err := json.Unmarshal(in.Writeback, &asMap); err != nil {
			return fmt.Errorf("SettleFromDelegate: exit writeback bytes not JSON-decodable: %w", err)
		}
	}
	parentRunID := *exitScope.ParentRunID
	parent, err := rt.GetByID(ctx, tx, parentRunID)
	if err != nil {
		return fmt.Errorf("SettleFromDelegate: load parent run %s: %w", parentRunID, err)
	}
	if parent == nil {
		return fmt.Errorf("SettleFromDelegate: parent run %s not found", parentRunID)
	}
	if args.Logger != nil {
		args.Logger.Info("subgraph: carry exit writeback to parent run",
			"exit_run_id", in.ExitRunID.String(),
			"parent_run_id", parentRunID.String(),
			"parent_node_id", parent.NodeID.String(),
			"writeback_field_count", len(asMap))
	}
	if args.Persist.NodeAttributes() == nil {
		return fmt.Errorf("SettleFromDelegate: NodeAttributes is required")
	}
	if len(in.Writeback) > 0 {
		if err := args.Persist.NodeAttributes().Upsert(
			ctx, parent.RunID, parent.NodeID, asMap, tx,
		); err != nil {
			return fmt.Errorf("SettleFromDelegate: upsert parent attributes: %w", err)
		}
	}
	// @concept: run-scope
	if err := scopes.Close(ctx, tx, exit.RunScopeID); err != nil {
		return fmt.Errorf("SettleFromDelegate: close sub-graph run scope %s: %w", exit.RunScopeID, err)
	}
	if instTbl, tplTbl := args.Persist.Instances(), args.Persist.Templates(); instTbl != nil && tplTbl != nil {
		if inst, err := instTbl.Get(ctx, exitScope.InstanceID, tx); err == nil && inst != nil {
			if tpl, err := tplTbl.GetByHash(ctx, inst.TemplateHash, tx); err == nil && tpl != nil {
				FanOutRunScopeEvent(ctx, args.Persist, args.LifecycleSubs,
					args.LifecyclePeersForSpec, tpl.Spec, exit.RunScopeID,
					exitScope.InstanceID, "subgraph_exit", tx)
			}
		}
	}
	// @concept: cascade
	if args.Persist.Nodes() == nil {
		return fmt.Errorf("SettleFromDelegate: Nodes table is required for the parent-settlement cascade bridge")
	}
	callingNodeRow, err := args.Persist.Nodes().Get(ctx, parent.NodeID, tx)
	if err != nil {
		return fmt.Errorf("SettleFromDelegate: load calling node: %w", err)
	}
	if callingNodeRow != nil && callingNodeRow.FrameID != nil {
		exitBridgeSig := signalpkg.Signal{
			Type: signalpkg.TypePath("terminal/success"),
			Payload: map[string]any{
				"changed":          true,
				"attributes_delta": orEmptyMap(asMap),
				"change_summary":   "subgraph_exit_carry",
			},
		}
		if err := cascadeSubscribersStaleInTx(ctx, args, tx,
			parent.NodeID, callingNodeRow.NodeType, parent.RunID,
			in.InstanceID, *callingNodeRow.FrameID, exitBridgeSig); err != nil {
			return fmt.Errorf("SettleFromDelegate: cascade subscribers of calling node: %w", err)
		}
		if err := emitAttributeChangesForRunInTx(ctx, args, tx,
			parent.NodeID, callingNodeRow.NodeType, parent.RunID, in.InstanceID, *callingNodeRow.FrameID,
			nil, nil); err != nil {
			return fmt.Errorf("SettleFromDelegate: emit parent attribute changes: %w", err)
		}
		if err := args.Persist.WaitSet().MarkDrainedBySender(ctx, *callingNodeRow.FrameID, parent.RunID, tx); err != nil {
			return fmt.Errorf("SettleFromDelegate: drain wait-set for calling node: %w", err)
		}
	}
	if args.Persist.Events() == nil {
		return fmt.Errorf("SettleFromDelegate: Events table is required for the exit-carry forensics record")
	}
	nodeID := in.ExitNodeID
	instanceID := in.InstanceID
	return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       events.KindSubgraphExitCarry(),
		Payload: map[string]any{
			"parent_run_id":   parentRunID.String(),
			"exit_run_id":     in.ExitRunID.String(),
			"exit_node_alias": in.ExitNodeAlias,
			"outcome":         "fresh",
		},
	}, tx)
}

// @concept: child-execution
// @concept: fan-out
// @concept: claim-tree
func SettleFromFanoutChild(
	ctx context.Context, args RunArgs, tx persistence.Tx, in FanoutChildSettlementInput,
) (postCommitFn, error) {
	parent, err := args.ClaimHandles.LockForUpdate(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, nil
	}
	if err := recordChildCommitMetadata(ctx, args, tx, in, parent); err != nil {
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
		pc, err := cancelInFlightSiblings(ctx, args, tx, in.ParentClaimHandleID, in.ChildClaimHandleID)
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
			childRun, err := args.Persist.RunTree().GetByID(ctx, tx, *c.NodeRunID)
			if err != nil {
				return nil, fmt.Errorf("SettleFromFanoutChild: load child run %s: %w", c.NodeRunID, err)
			}
			if childRun == nil {
				continue
			}
			childScope, err := scopes.GetByID(ctx, tx, childRun.RunScopeID)
			if err != nil {
				return nil, fmt.Errorf("SettleFromFanoutChild: load child run scope %s: %w", childRun.RunScopeID, err)
			}
			if childScope == nil || childScope.PartitionKey == "" {
				continue
			}
			if err := scopes.Close(ctx, tx, childRun.RunScopeID); err != nil {
				return nil, fmt.Errorf("SettleFromFanoutChild: close partition scope %s: %w", childRun.RunScopeID, err)
			}
			if instTbl, tplTbl := args.Persist.Instances(), args.Persist.Templates(); instTbl != nil && tplTbl != nil {
				if inst, err := instTbl.Get(ctx, childScope.InstanceID, tx); err == nil && inst != nil {
					if tpl, err := tplTbl.GetByHash(ctx, inst.TemplateHash, tx); err == nil && tpl != nil {
						FanOutRunScopeEvent(ctx, args.Persist, args.LifecycleSubs,
							args.LifecyclePeersForSpec, tpl.Spec, childRun.RunScopeID,
							childScope.InstanceID, "fanout_partition_terminal", tx)
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
	producer, ok := args.StoreRegistry.Get(producerName)
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
		parentHint.RunID = *parent.NodeRunID
	}
	if acquirer, aErr := args.Persist.Nodes().Get(ctx, parent.HolderNodeID, tx); aErr == nil && acquirer != nil {
		parentHint.InstanceID = acquirer.InstanceID
	}
	if *parent.HolderSupervisorID != args.SupervisorID {
		if err := args.ClaimHandles.ReassignHolderSupervisor(
			ctx, in.ParentClaimHandleID, *parent.HolderSupervisorID, args.SupervisorID, tx,
		); err != nil {
			return nil, fmt.Errorf("SettleFromFanoutChild: settlement takeover of parent %s (holder %s → %s): %w",
				in.ParentClaimHandleID, *parent.HolderSupervisorID, args.SupervisorID, err)
		}
	}
	pc, err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID:       in.ParentClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(parent.ClaimScopeData),
		Address:             []byte(parent.Address),
		Lifetime:            parent.Lifetime,
		CandidateHandle:     parent.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         parentHint,
		ParentClaimHandleID: parent.ParentClaimHandleID,
	})
	if err != nil {
		return nil, err
	}
	post = chainPostCommit(post, pc)
	return post, nil
}

func recordChildCommitMetadata(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	in FanoutChildSettlementInput, parent *persistence.ClaimHandleRow,
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
	key, err := childPartitionWritebackKey(ctx, args, tx, in.ChildClaimHandleID)
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
	ctx context.Context, args RunArgs, tx persistence.Tx, childID shared.UUID,
) (string, error) {
	fallback := childID.String()
	child, err := args.ClaimHandles.Get(ctx, childID, tx)
	if err != nil {
		return "", fmt.Errorf("load child claim handle %s: %w", childID, err)
	}
	if child == nil || child.NodeRunID == nil {
		return fallback, nil
	}
	childRun, err := args.Persist.RunTree().GetByID(ctx, tx, *child.NodeRunID)
	if err != nil {
		return "", fmt.Errorf("load child run %s: %w", child.NodeRunID, err)
	}
	if childRun == nil {
		return fallback, nil
	}
	scope, err := args.Persist.RunScopes().GetByID(ctx, tx, childRun.RunScopeID)
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
		if abandoned > policy.MaxFailures {
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
