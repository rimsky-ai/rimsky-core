// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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
	DispatchID shared.UUID
}

type RunArgs struct {
	Persist        persistence.Tables
	Queue          persistence.Queue
	AdvisoryLocker persistence.AdvisoryLocker
	ClaimHandles   persistence.ClaimHandleTable
	StoreRegistry  *locks.Registry
	NamedLocks     locks.NamedLocksConfig

	Clock             shared.Clock
	Logger            shared.Logger
	SupervisorID      string
	AcceptedExecutors []string
	AcceptedStores    []string

	Pool        *executor.ClientPool
	Resolver    executor.Resolver
	CallbackURL string

	LivenessInterval      time.Duration
	ResumeGrace           time.Duration
	SelectCandidatesLimit int

	Blob               persistence.BlobBackend
	BlobSpillThreshold int

	MaxRetriesWithoutProgressDefault int

	SyncRPCDeadlineDefault time.Duration

	MaxQuietPeriodDefault time.Duration

	MaxRuntimeDefault time.Duration

	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)

	// @concept: terminal-tag
	DeclaredTagsFor func(executorName string) (tags []string, ok bool)

	Metrics MetricsHook

	DataProcessors DataProcessingRegistry

	LifecycleSubs          *locks.LifecycleRegistry
	LifecyclePeersForSpec  func(tplSpec node.TemplateSpec) []string
	LateBindServiceProxies map[string]string

	PostCommitHook func(ctx context.Context)

	PreAcquireUnavailableHook func(ctx context.Context)

	CheckAndFireHook func(ctx context.Context)
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
	NodeID             shared.UUID
	InstanceID         shared.UUID
	DispatchID         shared.UUID
	SupervisorID       string
	StoreRegistry      *locks.Registry
	FrameID            shared.UUID
	AcquiredLocks      []AcquiredLock
	NodeType           string
	Executor           string
	NodeDef            *node.TemplateNodeDef
	ResolvedAttributes map[string]any
	AttributesSchema   map[string]any
}

type AcquiredLock struct {
	Spec                    any
	ClaimHandleID           shared.UUID
	ClaimResult             claimproducer.ClaimResult
	Producer                locks.ClaimProducer
	Alias                   string
	IsHeld                  bool
	ProducerCandidateHandle []byte
	UnavailableClass        string
}

// @concept: supervisor (this claim-and-execute cycle is the supervisor's
func RunNode(
	ctx context.Context,
	args RunArgs,
	registerAsync func(ackID string, actx AsyncContext),
) (RunnerResult, error) {
	if err := validateRunArgs(args); err != nil {
		return RunnerResult{}, err
	}
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
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
			return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
		}
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, nil
	}

	resolvedAttrs, attrSchema, err := resolveAttributes(ctx, args, &acq)
	if err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID},
			applyAttributeFailure(ctx, args, &acq, err)
	}

	if err := upsertAttributesPreDispatch(ctx, args, acq.DispatchID, acq.NodeID, resolvedAttrs); err != nil {
		log.Warn("runner: upsert attributes pre-dispatch failed",
			"run_id", acq.DispatchID.String(),
			"node_id", acq.NodeID.String(), "error", err.Error())
	}
	dispatchAttrs := resolvedAttrs

	dctx := dispatchContext{
		Args:             args,
		Acquired:         &acq,
		Attributes:       dispatchAttrs,
		AttributesSchema: attrSchema,
		LivenessInterval: livenessInterval,
		Log:              log,
		RegisterAsync:    registerAsync,
	}
	terminal, asyncResult, err := dispatch(ctx, dctx)
	if err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
	}
	if asyncResult != nil {
		return *asyncResult, nil
	}

	if err := runApplyTerminal(ctx, args, &acq, dispatchAttrs, attrSchema, terminal, nil); err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
	}

	scope := resolveAcqScope(ctx, args, &acq)
	terminalSig := signalForTerminal(terminal)
	if _, err := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		NodeID:           acq.NodeID,
		DispatchID:       acq.DispatchID,
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
			"dispatch_id", acq.DispatchID.String(),
			"error", err.Error())
	}
	return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, nil
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
	if args.StoreRegistry == nil {
		return errors.New("supervisor.RunNode: StoreRegistry is required")
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

func upsertAttributesPreDispatch(
	ctx context.Context,
	args RunArgs,
	runID, nodeID shared.UUID,
	resolvedAttrs map[string]any,
) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.NodeAttributes().Upsert(ctx, runID, nodeID, resolvedAttrs, tx)
	})
}

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
		eventKind, extractDirective(err), "attribute", "", err.Error())
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
		return "template_resolution_failed", events.KindTemplateResolutionFailed()
	}
	var schemaUnavail *executorSchemaUnavailableError
	if errors.As(err, &schemaUnavail) {
		return "executor_schema_unavailable", events.KindExecutorSchemaUnavailable()
	}
	var validation *attributeValidationError
	if errors.As(err, &validation) {
		return "template_validation_failed", events.KindTemplateValidationFailed()
	}
	return "template_resolution_failed", events.KindTemplateResolutionFailed()
}

func extractDirective(err error) string {
	var miss *attributes.ErrMissingSource
	if errors.As(err, &miss) {
		return miss.Directive
	}
	return ""
}
