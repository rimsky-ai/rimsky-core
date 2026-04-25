// Package supervisor is the Go port of rimsky/src/supervisor/* — the
// terminal-outcome funnel that turns an executor's run result into
// resource commits, event-log writes, policy-driven error handling, and
// dependent recalculation.
//
// Port of rimsky/src/supervisor/commit.ts (Plan A Task 10.1). Unlike the
// TS reference, the Go port delegates quality-rule evaluation to the
// Resource implementation (Resource.Commit) — the supervisor no longer
// reaches into per-node quality_rules from the template.
package supervisor

import (
	"context"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// CommitArgs carries everything Commit needs to run the commit flow for a
// successfully-executed node. GetResource maps an owned-resource row to a
// concrete resource.Resource (constructed at instance-provisioning time and
// held by the supervisor's resource registry; tests inject a callback).
type CommitArgs struct {
	Storage    storage.StorageBackend
	Queue      queue.DispatchQueue
	Clock      shared.Clock
	Logger     shared.Logger
	NodeID     shared.UUID
	InstanceID shared.UUID
	// SupervisorID identifies the supervisor currently holding the claim.
	// Propagated to any OnError routing so RemoveForNode is claimant-guarded.
	SupervisorID  string
	Result        any
	Changed       bool
	ChangeSummary string
	GetResource   func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error)
}

// Commit runs the commit flow for a successfully-executed node. If the node
// owns resources each is committed through its Resource; quality errors route
// to OnError(quality_rule_failed). On success (or when the node owns no
// resources) a work_completed event is appended, error-tracking is cleared,
// the node transitions to fresh via work_completed, and — when Changed is
// true — every dependent receives a recalculate.
//
// Port of rimsky/src/supervisor/commit.ts + the cascade section of
// rimsky/src/supervisor/terminal-outcome.ts.
func Commit(ctx context.Context, args CommitArgs) error {
	sb, log := args.Storage, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	resources, err := sb.Resources().ListByOwner(ctx, args.NodeID, nil)
	if err != nil {
		return err
	}

	if len(resources) > 0 {
		for _, row := range resources {
			if err := commitOneResource(ctx, args, row, log); err != nil {
				return err
			}
			// commitOneResource returns nil on quality failure after routing
			// to OnError; we still must NOT continue to work_completed in
			// that case. Sentinel: inspect node state — if it is no longer
			// running we know OnError took over. Simpler: check flag via
			// nd lookup below.
		}
		// After per-resource processing, if OnError routed the node to
		// stale/failed we should NOT append work_completed. Detect by
		// re-loading the node.
		nd, nerr := sb.Nodes().Get(ctx, args.NodeID, nil)
		if nerr != nil {
			return nerr
		}
		if nd == nil || nd.State != shared.NodeStateRunning {
			return nil
		}
	}

	// All resources committed cleanly OR node owns none — finalize.
	outcome := "committed"
	if !args.Changed {
		outcome = "no_op"
	}
	_ = sb.Events().Append(ctx, storage.EventAppendInput{
		InstanceID: &args.InstanceID,
		NodeID:     &args.NodeID,
		Kind:       "work_completed",
		Payload:    map[string]any{"outcome": outcome},
	}, nil)

	// Clear error tracking and transition to fresh.
	if err := sb.Nodes().UpdateError(ctx, args.NodeID, node.EvaluatorState{
		ActionIndex: 0, RetryCounter: 0, CurrentErrorClass: "",
	}, nil); err != nil {
		log.Warn("Commit: UpdateError reset failed",
			"node_id", args.NodeID.String(), "error", err.Error())
	}
	if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateFresh, node.ReasonWorkCompleted, nil); err != nil {
		return err
	}

	// Cascade on real changes only.
	if args.Changed {
		dependents, err := sb.Nodes().ListDependentsOf(ctx, args.NodeID, nil)
		if err != nil {
			return err
		}
		for _, dep := range dependents {
			src := args.NodeID
			_ = scheduler.RecalculateNode(ctx, scheduler.RecalculateArgs{
				Storage:      sb,
				Queue:        args.Queue,
				Clock:        args.Clock,
				Logger:       log,
				SourceNodeID: &src,
				TargetNodeID: dep.ID,
			})
		}
	}
	return nil
}

// commitOneResource drives one owned resource through Resource.Commit,
// logging commit / no_op_commit events on success and routing to OnError
// on quality rejection.
func commitOneResource(ctx context.Context, args CommitArgs, row storage.ResourceRow, log shared.Logger) error {
	sb := args.Storage
	r, err := args.GetResource(ctx, row.ID)
	if err != nil {
		log.Warn("Commit: resolve resource failed",
			"resource_id", row.ID.String(), "error", err.Error())
		return OnError(ctx, OnErrorArgs{
			Storage: sb, Queue: args.Queue, Clock: args.Clock, Logger: log,
			NodeID: args.NodeID, InstanceID: args.InstanceID,
			SupervisorID: args.SupervisorID,
			ErrorClass:   "resource_resolve_failed",
			Payload:      map[string]any{"error": err.Error()},
		})
	}
	result, err := r.Commit(ctx, resource.CommitRequest{
		ProducedBy:    args.NodeID,
		Result:        args.Result,
		Changed:       args.Changed,
		ChangeSummary: args.ChangeSummary,
	})
	if err != nil {
		log.Warn("Commit: resource.Commit errored",
			"resource_id", row.ID.String(), "error", err.Error())
		return OnError(ctx, OnErrorArgs{
			Storage: sb, Queue: args.Queue, Clock: args.Clock, Logger: log,
			NodeID: args.NodeID, InstanceID: args.InstanceID,
			SupervisorID: args.SupervisorID,
			ErrorClass:   "commit_failed",
			Payload:      map[string]any{"error": err.Error()},
		})
	}
	if !result.Accepted {
		for _, f := range result.QualityErrors {
			_ = sb.Events().Append(ctx, storage.EventAppendInput{
				InstanceID: &args.InstanceID,
				NodeID:     &args.NodeID,
				Kind:       "quality_rule_failed",
				Payload: map[string]any{
					"rule_type":   f.RuleType,
					"rule_config": f.Config,
					"severity":    string(f.Severity),
					"details":     f.Details,
				},
			}, nil)
		}
		return OnError(ctx, OnErrorArgs{
			Storage: sb, Queue: args.Queue, Clock: args.Clock, Logger: log,
			NodeID: args.NodeID, InstanceID: args.InstanceID,
			SupervisorID: args.SupervisorID,
			ErrorClass:   "quality_rule_failed",
			Payload:      map[string]any{"errors": result.QualityErrors},
		})
	}
	// Accepted. Log commit or no_op_commit.
	if args.Changed && result.Version != nil {
		_ = sb.Events().Append(ctx, storage.EventAppendInput{
			InstanceID: &args.InstanceID,
			NodeID:     &args.NodeID,
			Kind:       "commit",
			Payload: map[string]any{
				"resource_id":    row.ID.String(),
				"version_id":     result.Version.ID.String(),
				"change_summary": args.ChangeSummary,
			},
		}, nil)
	} else {
		_ = sb.Events().Append(ctx, storage.EventAppendInput{
			InstanceID: &args.InstanceID,
			NodeID:     &args.NodeID,
			Kind:       "no_op_commit",
			Payload:    map[string]any{"reason": args.ChangeSummary},
		}, nil)
	}
	return nil
}
