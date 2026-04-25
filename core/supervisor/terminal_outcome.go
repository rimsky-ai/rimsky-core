// Port of rimsky/src/supervisor/terminal-outcome.ts (Plan A Task 10.1).
// ApplyTerminalOutcome is the single entry point from the supervisor's
// runner when an executor stream terminates. It classifies the terminal
// state and routes to Commit or OnError.
package supervisor

import (
	"context"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// TerminalKind classifies an executor's exit.
type TerminalKind int

const (
	// TerminalRunSucceeded indicates the handler/agent produced a result.
	TerminalRunSucceeded TerminalKind = iota
	// TerminalAppError is an application-level failure with a declared error class.
	TerminalAppError
	// TerminalInfraError is an infra-layer failure (silence_timeout,
	// subprocess_exit_before_complete, supervisor_crash, operator_kill, ...).
	// Spec §7.2: these are "restart events, not application retries" and
	// therefore bypass the error-class policy chain.
	TerminalInfraError
)

// TerminalOutcome is the classification of a node run's terminal state.
type TerminalOutcome struct {
	Kind          TerminalKind
	Result        any // for RunSucceeded
	Changed       bool
	ChangeSummary string
	ErrorClass    string // for AppError / InfraError
	Payload       map[string]any
}

// ApplyTerminalArgs is the payload for ApplyTerminalOutcome.
type ApplyTerminalArgs struct {
	Storage    storage.StorageBackend
	Queue      queue.DispatchQueue
	Clock      shared.Clock
	Logger     shared.Logger
	NodeID     shared.UUID
	InstanceID shared.UUID
	// SupervisorID identifies the supervisor currently holding the claim on
	// this node. Threaded through to RemoveForNode / OnError so the queue
	// delete can be claimant-guarded; empty string falls back to unguarded
	// delete (tests without dispatch rows, backward compat).
	SupervisorID string
	GetResource  func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error)
	Outcome      TerminalOutcome
}

// ApplyTerminalOutcome routes a terminal executor outcome to the appropriate
// commit / error-handling path.
func ApplyTerminalOutcome(ctx context.Context, args ApplyTerminalArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	switch args.Outcome.Kind {
	case TerminalRunSucceeded:
		return Commit(ctx, CommitArgs{
			Storage: args.Storage, Queue: args.Queue, Clock: args.Clock, Logger: log,
			NodeID: args.NodeID, InstanceID: args.InstanceID,
			SupervisorID:  args.SupervisorID,
			Result:        args.Outcome.Result,
			Changed:       args.Outcome.Changed,
			ChangeSummary: args.Outcome.ChangeSummary,
			GetResource:   args.GetResource,
		})
	case TerminalAppError:
		return OnError(ctx, OnErrorArgs{
			Storage: args.Storage, Queue: args.Queue, Clock: args.Clock, Logger: log,
			NodeID: args.NodeID, InstanceID: args.InstanceID,
			SupervisorID: args.SupervisorID,
			ErrorClass:   args.Outcome.ErrorClass,
			Payload:      args.Outcome.Payload,
		})
	case TerminalInfraError:
		nd, err := args.Storage.Nodes().Get(ctx, args.NodeID, nil)
		if err != nil {
			return err
		}
		if nd == nil {
			return nil
		}
		_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
			InstanceID: &args.InstanceID,
			NodeID:     &args.NodeID,
			Kind:       "error",
			Payload: map[string]any{
				"error_class":  args.Outcome.ErrorClass,
				"details":      args.Outcome.Payload,
				"action_taken": "infra_reenqueue",
			},
		}, nil)
		if nd.State == shared.NodeStateRunning {
			// infra_reenqueue transitions running→stale without bumping retry.
			// Distinguishes an explicit terminal-path re-enqueue from the
			// scheduler's heartbeat-lost sweep (which uses ReasonHeartbeatLost).
			_ = args.Storage.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateStale, node.ReasonInfraReenqueue, nil)
		}
		_ = args.Queue.RemoveForNode(ctx, args.NodeID, args.SupervisorID)
		return args.Queue.Enqueue(ctx, queue.DispatchRequest{
			NodeID:          args.NodeID,
			ExecutorName:    nd.Executor,
			ConcurrencyTags: nd.ConcurrencyTags,
			EnqueuedAt:      args.Clock.Now(),
		})
	}
	return nil
}
