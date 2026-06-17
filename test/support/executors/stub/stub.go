// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package stub is a test-double Executor implementation in the
// Meszaros sense — scripted canned outcomes for tests and conformance.
// NOT a skeleton template for writing your own executor; implement the
// Executor gRPC service against protocols/proto/v1/executor.proto.
//
// Per TD-execute-rpc-unary the Executor.Execute RPC is unary; the
// stub returns the settling Outcome directly.
//
// Two primary uses:
//   - executors/stub/stubtest — wrapper for in-process scenario tests
//     in test/scenarios/. Tests script per-node-type behavior via
//     Stub.WhenType("…").Success/Error/Park/… and assert on the
//     supervisor's reaction.
//   - rimsky-executor-conformance — known-good target for protocol
//     conformance checks (when run with --require-stub-mode).
//
// EnableStubMode shortcuts scripted behavior with immediate-success
// outcomes plus StubAttributesFor(node_type)-shaped attributes_delta;
// the conformance harness uses this mode.
package stub

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// parkReasonFromStorageForm maps the lower_snake_case storage form
// (e.g. "await_callback") back to the proto enum value. Unknown
// inputs (including empty) fall back to PARK_REASON_AWAIT_CALLBACK,
// the safer default in the closed two-value set (no auto-resume).
// Mirrors the runtime helper of the same name to keep the stub
// self-contained.
func parkReasonFromStorageForm(s string) genv1.ParkReason {
	if s == "" {
		return genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK
	}
	upper := "PARK_REASON_" + strings.ToUpper(s)
	if v, ok := genv1.ParkReason_value[upper]; ok {
		return genv1.ParkReason(v)
	}
	return genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK
}

type terminalKind int

const (
	termSuccess terminalKind = iota
	termError
	termAsync
	// @deliberate: termPark scripts a Park outcome (parking the node until
	// resume_at).
	termPark
)

type script struct {
	terminal          terminalKind
	result            any
	changed           bool
	changeSum         string
	errorClass        string
	payload           any
	asyncAckID        string
	asyncCompletionMs int64
	delay             time.Duration
	parkReason        genv1.ParkReason
	parkReasonNote    string
	parkResumeAt      time.Time // @deliberate: zero ⇒ indefinite park (no resume_at)
	// tags are the executor-attached subscriber-visible discriminators
	// per concept:terminal-tag, threaded onto Success/Error/Park.
	tags []string
	// @deliberate: holdUntil, when non-nil, blocks the run until the
	// channel closes (or the call context cancels). Lets eligibility
	// tests hold a sender in-flight at a deterministic midpoint
	// instead of racing wall-clock delays.
	holdUntil <-chan struct{}
}

// Stub is a scripted Executor server for tests.
type Stub struct {
	genv1.UnimplementedExecutorServer
	mu       sync.Mutex
	scripts  map[string]*script
	stubMode bool
	observed []ObservedRequest
}

// ObservedRequest captures the dispatch-time fields a test may want to assert
// against. Under the 2026-05-21 userdata collapse the executor receives a
// single unified attribute bag (source-resolved + static-default + post-
// merge L3/L4 overrides) — recorded here per call so tests can verify the
// supervisor wired the bag through correctly.
//
// CallbackURL and CancelToken are recorded so scenario tests exercising
// the §12.5 incremental-writeback path can POST per-field deltas back to
// the supervisor with the same auth shape a real executor would use.
type ObservedRequest struct {
	NodeID                   string
	InstanceID               string
	NodeType                 string
	Attributes               map[string]any
	CallbackURL              string
	CancelToken              string
	DispatchID               string
	PriorDispatchID          string                         // @deliberate: empty when unset on the wire
	PriorDispatchDisposition genv1.PriorDispatchDisposition // @deliberate: PRIOR_NONE when unset on the wire
	// CandidateHandles records the per-store-alias candidate_handle bytes
	// carried on each ExecuteRequest.StoreHandle.
	CandidateHandles map[string][]byte
}

// New constructs a Stub with no scripted node types registered.
func New() *Stub { return &Stub{scripts: map[string]*script{}} }

// EnableStubMode switches the Stub into immediate-success mode.
func (s *Stub) EnableStubMode() *Stub {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stubMode = true
	return s
}

// Observed returns a snapshot of every ExecuteRequest the stub has seen since
// construction. Safe to call concurrently with in-flight Execute calls.
func (s *Stub) Observed() []ObservedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ObservedRequest, len(s.observed))
	copy(out, s.observed)
	return out
}

// TypeBuilder registers scripted behavior for nodes of a specific type.
type TypeBuilder struct {
	s   *Stub
	typ string
}

// WhenType begins scripting behavior for the given node_type. Default
// terminal is a Success outcome with changed=true.
func (s *Stub) WhenType(t string) *TypeBuilder {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := &script{terminal: termSuccess, changed: true}
	s.scripts[t] = sc
	return &TypeBuilder{s: s, typ: t}
}

// Success configures the scripted terminal as a Success outcome on the wire.
func (b *TypeBuilder) Success(result any, changed bool, changeSummary string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.result, sc.changed, sc.changeSum = termSuccess, result, changed, changeSummary
	return b
}

// Error configures the scripted terminal as an Error outcome on the
// wire. Per `concept:signal` hierarchical convention, the stub prefixes
// single-segment classes with `stub/` at emit time.
func (b *TypeBuilder) Error(class string, payload any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.errorClass, sc.payload = termError, class, payload
	return b
}

// AwaitAsyncCallback configures the scripted terminal as an AwaitAsyncCallback outcome.
func (b *TypeBuilder) AwaitAsyncCallback(ackID string, completionMs int64) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.asyncAckID, sc.asyncCompletionMs = termAsync, ackID, completionMs
	return b
}

// Park configures the scripted terminal as a Park outcome. Per
// TD-remove-resume-context, Park carries no session_token / payload
// bytes — resume state rides attribute carry-forward.
func (b *TypeBuilder) Park(reason genv1.ParkReason, reasonNote string, resumeAt time.Time) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal = termPark
	sc.parkReason = reason
	sc.parkReasonNote = reasonNote
	sc.parkResumeAt = resumeAt
	return b
}

// Tags scripts the executor-attached tags on the settling outcome
// (concept:terminal-tag). Duplicates are collapsed at decode by the
// supervisor.
func (b *TypeBuilder) Tags(tags ...string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].tags = append([]string(nil), tags...)
	return b
}

// HoldUntil blocks each run of this node type until ch closes (or the
// call context cancels). Gives dispatch-eligibility tests a
// deterministic midpoint.
func (b *TypeBuilder) HoldUntil(ch <-chan struct{}) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].holdUntil = ch
	return b
}

// Delay sleeps for d before returning the outcome.
func (b *TypeBuilder) Delay(d time.Duration) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].delay = d
	return b
}

// Execute implements genv1.ExecutorServer with a unary RPC returning
// the scripted Outcome.
func (s *Stub) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	var candidateHandles map[string][]byte
	if len(req.GetStores()) > 0 {
		candidateHandles = make(map[string][]byte, len(req.GetStores()))
		for alias, sh := range req.GetStores() {
			if ch := sh.GetCandidateHandle(); len(ch) > 0 {
				candidateHandles[alias] = append([]byte(nil), ch...)
			}
		}
	}
	s.mu.Lock()
	s.observed = append(s.observed, ObservedRequest{
		NodeID:                   req.GetNodeId(),
		InstanceID:               req.GetInstanceId(),
		NodeType:                 req.GetNodeType(),
		Attributes:               req.GetAttributes().AsMap(),
		CallbackURL:              req.GetCallbackUrl(),
		CancelToken:              req.GetCancelToken(),
		DispatchID:               req.GetDispatchId(),
		PriorDispatchID:          req.GetPriorDispatchId(),
		PriorDispatchDisposition: req.GetPriorDispatchDisposition(),
		CandidateHandles:         candidateHandles,
	})
	stubMode := s.stubMode
	sc, ok := s.scripts[req.NodeType]
	s.mu.Unlock()

	if stubMode {
		attrs := req.GetAttributes().AsMap()
		if probe, _ := attrs["probe_park"].(bool); probe {
			reasonStr, _ := attrs["park_reason"].(string)
			reasonLabel, _ := attrs["park_reason_label"].(string)
			reasonNote, _ := attrs["park_reason_note"].(string)
			park := &genv1.Park{
				Reason:      parkReasonFromStorageForm(reasonStr),
				ReasonLabel: reasonLabel,
				ReasonNote:  reasonNote,
			}
			return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: park}}, nil
		}
		delta, err := structpb.NewStruct(StubAttributesFor(req.GetNodeType()))
		if err != nil {
			return nil, err
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         true,
			ChangeSummary:   "stub",
		}}}, nil
	}

	if !ok {
		return nil, fmt.Errorf("stub: no script for node_type %q", req.NodeType)
	}

	if sc.delay > 0 {
		select {
		case <-time.After(sc.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if sc.holdUntil != nil {
		select {
		case <-sc.holdUntil:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	switch sc.terminal {
	case termSuccess:
		delta, err := toStruct(sc.result)
		if err != nil {
			return nil, err
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         sc.changed,
			ChangeSummary:   sc.changeSum,
			Tags:            sc.tags,
		}}}, nil
	case termError:
		v, err := toStruct(sc.payload)
		if err != nil {
			return nil, err
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			// @deliberate: 2026-05-23 signal-taxonomy Pass 6: prefix
			// unscoped classes with `stub/` so the stub follows the
			// hierarchical-class convention.
			ErrorClass: prefixedStubClass(sc.errorClass),
			Payload:    v,
			Tags:       sc.tags,
		}}}, nil
	case termAsync:
		return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId:           sc.asyncAckID,
			ExpectedCompletionMs: sc.asyncCompletionMs,
		}}}, nil
	case termPark:
		park := &genv1.Park{
			Reason:     sc.parkReason,
			ReasonNote: sc.parkReasonNote,
			Tags:       sc.tags,
		}
		if !sc.parkResumeAt.IsZero() {
			park.ResumeAt = timestamppb.New(sc.parkResumeAt)
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: park}}, nil
	}
	return nil, fmt.Errorf("stub: unknown terminal kind %d", sc.terminal)
}

// stubFixtures maps node_type to a default attributes_delta the stub
// returns when stub mode is enabled.
var stubFixtures = map[string]map[string]any{
	"items.fetch":    {"items": []any{}, "fetched_at": "1970-01-01T00:00:00Z"},
	"items.classify": {"category": "unclassified"},
}

// StubAttributesFor returns the default attributes_delta for a node_type
// when the stub is running in stub mode.
func StubAttributesFor(nodeType string) map[string]any {
	src, ok := stubFixtures[nodeType]
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// prefixedStubClass returns class unchanged if it already contains a
// `/` (operator-supplied hierarchical class) or is empty; otherwise
// it returns `stub/<class>`.
func prefixedStubClass(class string) string {
	if class == "" {
		return class
	}
	if strings.Contains(class, "/") {
		return class
	}
	return "stub/" + class
}

// toStruct converts an arbitrary input into a structpb.Struct for use
// as Success.AttributesDelta or Error.Payload.
func toStruct(v any) (*structpb.Struct, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		// @deliberate: Wrap non-map scalars under a single "value" field so the test
		// fixture can still observe them when needed.
		return structpb.NewStruct(map[string]any{"value": fmt.Sprintf("%v", v)})
	}
	return structpb.NewStruct(m)
}
