// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	parkResumeAt      time.Time
	tags []string
	holdUntil <-chan struct{}
}

type Stub struct {
	genv1.UnimplementedExecutorServer
	mu       sync.Mutex
	scripts  map[string]*script
	stubMode bool
	observed []ObservedRequest
}

type ObservedRequest struct {
	NodeID                   string
	InstanceID               string
	NodeType                 string
	Attributes               map[string]any
	CallbackURL              string
	CancelToken              string
	DispatchID               string
	PriorDispatchID          string
	PriorDispatchDisposition genv1.PriorDispatchDisposition
	CandidateHandles map[string][]byte
}

func New() *Stub { return &Stub{scripts: map[string]*script{}} }

func (s *Stub) EnableStubMode() *Stub {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stubMode = true
	return s
}

func (s *Stub) Observed() []ObservedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ObservedRequest, len(s.observed))
	copy(out, s.observed)
	return out
}

type TypeBuilder struct {
	s   *Stub
	typ string
}

func (s *Stub) WhenType(t string) *TypeBuilder {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := &script{terminal: termSuccess, changed: true}
	s.scripts[t] = sc
	return &TypeBuilder{s: s, typ: t}
}

func (b *TypeBuilder) Success(result any, changed bool, changeSummary string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.result, sc.changed, sc.changeSum = termSuccess, result, changed, changeSummary
	return b
}

func (b *TypeBuilder) Error(class string, payload any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.errorClass, sc.payload = termError, class, payload
	return b
}

func (b *TypeBuilder) AwaitAsyncCallback(ackID string, completionMs int64) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.asyncAckID, sc.asyncCompletionMs = termAsync, ackID, completionMs
	return b
}

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

func (b *TypeBuilder) Tags(tags ...string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].tags = append([]string(nil), tags...)
	return b
}

func (b *TypeBuilder) HoldUntil(ch <-chan struct{}) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].holdUntil = ch
	return b
}

func (b *TypeBuilder) Delay(d time.Duration) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].delay = d
	return b
}

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

var stubFixtures = map[string]map[string]any{
	"items.fetch":    {"items": []any{}, "fetched_at": "1970-01-01T00:00:00Z"},
	"items.classify": {"category": "unclassified"},
}

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

func prefixedStubClass(class string) string {
	if class == "" {
		return class
	}
	if strings.Contains(class, "/") {
		return class
	}
	return "stub/" + class
}

func toStruct(v any) (*structpb.Struct, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return structpb.NewStruct(map[string]any{"value": fmt.Sprintf("%v", v)})
	}
	return structpb.NewStruct(m)
}
