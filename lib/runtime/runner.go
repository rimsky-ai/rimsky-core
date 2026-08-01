// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

type RunnerResult struct {
	Ran        bool
	Async      bool
	AsyncAckID string
	NodeID     shared.UUID
	NodeRunID  shared.UUID
}

type RunArgs struct {
	Persist               persistence.Tables
	Queue                 persistence.Queue
	AdvisoryLocker        persistence.AdvisoryLocker
	ClaimHandles          persistence.ClaimHandleTable
	ClaimProducerRegistry *locks.Registry
	NamedLocks            locks.NamedLocksConfig

	VerbOutbox       persistence.ProducerVerbOutboxTable
	ProducerVerbKick func()

	Clock        shared.Clock
	Logger       shared.Logger
	SupervisorID string

	Pool        *executor.ClientPool
	Resolver    executor.Resolver
	CallbackURL string

	LivenessInterval      time.Duration
	ResumeGrace           time.Duration
	SelectCandidatesLimit int

	Blob               persistence.BlobBackend
	BlobSpillThreshold int

	SyncRPCDeadlineDefault time.Duration

	MaxQuietPeriodDefault time.Duration

	MaxRuntimeDefault time.Duration

	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)

	// @concept: terminal-tag
	DeclaredTagsFor func(executorName string) (tags []string, ok bool)

	Metrics MetricsHook

	DataProcessors DataProcessingRegistry

	LifecycleSubs          *lifecycle.Registry
	LifecyclePeersForSpec  func(tplSpec node.TemplateSpec) []string
	LateBindServiceProxies map[string]string

	// @decision: race-injection-hooks
	PostCommitHook func(ctx context.Context)

	PreAcquireUnavailableHook func(ctx context.Context)

	CheckAndFireHook func(ctx context.Context)

	// @concept: fan-out
	FanOutSemaphores *FanOutSemaphoreRegistry
}

type MetricsHook interface {
	IncDispatch(executor, terminalClass string)
	IncTerminal(terminalClass, errorClass string)
	IncInvalidate(sourceKind string)
	IncClaimAcquisition(producer, intent string)
	IncNamedLockAcquisition(lockName, intent string)
	ObserveDispatchLatency(executor string, seconds float64)
	ObserveClaimAcquisitionLatency(producer string, seconds float64)
	ObserveFrameDuration(seconds float64)
	ObserveParkedDurationOnResume(seconds float64)
}

type noopMetrics struct{}

func (noopMetrics) IncDispatch(string, string)                     {}
func (noopMetrics) IncTerminal(string, string)                     {}
func (noopMetrics) IncInvalidate(string)                           {}
func (noopMetrics) IncClaimAcquisition(string, string)             {}
func (noopMetrics) IncNamedLockAcquisition(string, string)         {}
func (noopMetrics) ObserveDispatchLatency(string, float64)         {}
func (noopMetrics) ObserveClaimAcquisitionLatency(string, float64) {}
func (noopMetrics) ObserveFrameDuration(float64)                   {}
func (noopMetrics) ObserveParkedDurationOnResume(float64)          {}

func metricsOf(args RunArgs) MetricsHook {
	if args.Metrics == nil {
		return noopMetrics{}
	}
	return args.Metrics
}

type AsyncContext struct {
	NodeID                shared.UUID
	InstanceID            shared.UUID
	NodeRunID             shared.UUID
	SupervisorID          string
	ClaimProducerRegistry *locks.Registry
	FrameID               shared.UUID
	AcquiredLocks         []AcquiredLock
	NodeType              string
	Executor              string
	NodeDef               *node.TemplateNodeDef
	GraphName             string
	ResolvedAttributes    map[string]any
	AttributesSchema      map[string]any
	AsyncAckPrincipal     string
}

type AcquiredLock struct {
	Spec                    any
	ClaimHandleID           shared.UUID
	ClaimResult             claimproducer.ClaimResult
	Producer                locks.ClaimProducer
	Alias                   string
	IsHeld                  bool
	ProducerCandidateHandle []byte
	ProducerLeaseToken      string
	UnavailableClass        string
}

// @concept: supervisor
func RunNode(
	ctx context.Context,
	args RunArgs,
	registerAsync func(ackID string, actx AsyncContext) bool,
) (RunnerResult, error) {
	if err := validateRunArgs(args); err != nil {
		return RunnerResult{}, err
	}
	if args.Logger == nil {
		args.Logger = shared.SilentLogger{}
	}
	log := args.Logger
	livenessInterval := args.LivenessInterval
	if livenessInterval <= 0 {
		livenessInterval = 5 * time.Second
	}

	acq, ok, err := acquireCandidate(ctx, args, livenessInterval)
	if err != nil {
		return RunnerResult{}, err
	}
	if !ok {
		return RunnerResult{Ran: false}, nil
	}

	// @concept: fan-out
	// @concept: run-scope
	if len(acq.SubClaims) > 0 && IsFanOutNode(acq.NodeDef) {
		if err := dispatchFanOutChildren(ctx, args, &acq); err != nil {
			return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, err
		}
		return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, nil
	}

	var (
		resolvedAttrs map[string]any
		attrSchema    map[string]any
	)
	if acq.Executor != "" {
		var err error
		resolvedAttrs, attrSchema, err = resolveAttributes(ctx, args, &acq)
		if err != nil {
			return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID},
				applyAttributeFailure(ctx, args, &acq, err)
		}
	}
	dispatchAttrs := resolvedAttrs

	dctx := dispatchContext{
		Args:             args,
		Acquired:         &acq,
		Attributes:       dispatchAttrs,
		AttributesSchema: attrSchema,
		RegisterAsync:    registerAsync,
	}
	// @concept: error-policy
	var (
		terminal    terminalEvent
		asyncResult *RunnerResult
	)
	for {
		acq.RetryDecision = nil
		terminal, asyncResult, err = dispatch(ctx, dctx)
		if err != nil {
			return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, err
		}
		if asyncResult != nil {
			return *asyncResult, nil
		}
		if err := runApplyTerminal(ctx, args, &acq, dispatchAttrs, attrSchema, terminal, nil); err != nil {
			return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, err
		}
		if acq.RetryDecision != nil && acq.RetryDecision.IsReleaseAndRequeue() {
			return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, nil
		}
		if acq.RetryDecision == nil || !acq.RetryDecision.IsRetry() {
			break
		}
		if len(terminal.Scratch) > 0 {
			acq.Scratch = terminal.Scratch
		}
		if delay := time.Duration(acq.RetryDecision.DelayMs) * time.Millisecond; delay > 0 {
			select {
			case <-ctx.Done():
				return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	scope := resolveAcqScope(ctx, args, &acq)
	terminalSig := signalForTerminal(args, &acq, terminal)
	if _, err := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		NodeID:           acq.NodeID,
		NodeRunID:        acq.NodeRunID,
		FrameID:          acq.FrameID,
		Executor:         acq.Executor,
		NodeType:         acq.NodeType,
		Graph:            acq.GraphName,
		ChildKey:         scope.PartitionKey,
		MergedAttributes: acq.MergedAttributes,
		Checkpoint:       persistence.CheckpointAfterTerminal,
		TerminalSignal:   &terminalSig,
		NodeRunSnapshot:  nodeRunSnapshotForBreakpoint(&acq),
		HeldClaims:       heldClaimsSummaryForBreakpoint(&acq),
		OpenWaitSet:      openWaitSetSummaryForBreakpoint(ctx, args, &acq),
	}); err != nil && log != nil {
		log.Warn("breakpoint: after_terminal eval failed; continuing",
			"dispatch_id", acq.NodeRunID.String(),
			"error", err.Error())
	}
	return RunnerResult{Ran: true, NodeID: acq.NodeID, NodeRunID: acq.NodeRunID}, nil
}

func validateRunArgs(args RunArgs) error {
	if args.Persist == nil {
		return errors.New("supervisor.RunNode: Persist is required")
	}
	if args.Queue == nil {
		return errors.New("supervisor.RunNode: Queue is required")
	}
	if args.AdvisoryLocker == nil {
		return errors.New("supervisor.RunNode: AdvisoryLocker is required")
	}
	if args.ClaimHandles == nil {
		return errors.New("supervisor.RunNode: ClaimHandles is required")
	}
	if args.ClaimProducerRegistry == nil {
		return errors.New("supervisor.RunNode: ClaimProducerRegistry is required")
	}
	if args.SupervisorID == "" {
		return errors.New("supervisor.RunNode: SupervisorID is required")
	}
	if args.Resolver == nil {
		return errors.New("supervisor.RunNode: Resolver is required")
	}
	if args.Pool == nil {
		return errors.New("supervisor.RunNode: Pool (executor.ClientPool) is required")
	}
	return nil
}

const (
	substitutionSiteAttribute = "attribute"
	substitutionSiteLockName  = "lock_name"
	substitutionSiteScope     = "scope"
)

func emitAttributeFailureEvent(
	ctx context.Context, args RunArgs, nodeID, instanceID shared.UUID, kind events.Kind, directive, site, field, reason string,
) {
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &nodeID, InstanceID: &instanceID,
			Kind: kind,
			Payload: map[string]any{
				"directive": directive,
				"site":      site,
				"field":     field,
				"reason":    reason,
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("emitAttributeFailureEvent: append event failed",
			"node_id", nodeID.String(),
			"instance_id", instanceID.String(),
			"kind", kind.String(),
			"directive", directive,
			"error", err.Error())
	}
}

func applyAttributeFailure(
	ctx context.Context, args RunArgs, acq *acquisition, err error,
) error {
	class, eventKind := classifyAttributeFailure(err)
	emitAttributeFailureEvent(ctx, args, acq.NodeID, acq.InstanceID,
		eventKind, extractDirective(err), substitutionSiteAttribute, "", err.Error())
	var postCommit postCommitFn
	if txErr := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, perr := applyErrorPolicy(ctx, args, acq, class,
			map[string]any{"error": err.Error()}, tx)
		if perr != nil {
			return perr
		}
		postCommit = pc
		return nil
	}); txErr != nil {
		return txErr
	}
	if postCommit != nil {
		postCommit(ctx)
	}
	return nil
}

func classifyAttributeFailure(err error) (string, events.Kind) {
	var miss *attributes.ErrMissingSource
	if errors.As(err, &miss) {
		return spec.ErrorClassTemplateResolutionFailed, events.KindTemplateResolutionFailed()
	}
	var schemaUnavail *executorSchemaUnavailableError
	if errors.As(err, &schemaUnavail) {
		return spec.ErrorClassExecutorSchemaUnavailable, events.KindExecutorSchemaUnavailable()
	}
	var validation *attributeValidationError
	if errors.As(err, &validation) {
		return spec.ErrorClassTemplateValidationFailed, events.KindTemplateValidationFailed()
	}
	return spec.ErrorClassTemplateResolutionFailed, events.KindTemplateResolutionFailed()
}

func extractDirective(err error) string {
	var miss *attributes.ErrMissingSource
	if errors.As(err, &miss) {
		return miss.Directive
	}
	return ""
}
