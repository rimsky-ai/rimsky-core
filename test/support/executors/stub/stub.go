// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

var cancelProbeHTTPClient = &http.Client{Timeout: 5 * time.Second}

var asyncProbeHTTPClient = &http.Client{Timeout: 5 * time.Second}

const (
	asyncProbeCallbackMaxAttempts = 5
	asyncProbeCallbackBaseDelay   = 200 * time.Millisecond
)

func postAsyncProbeCallback(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	body, err := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": stubmode.ResponseDelta(),
			"changed":          false,
			"change_summary":   "stub async probe settle",
		},
	})
	if err != nil {
		return
	}
	url := callbackURL + "/v1/callback/" + ackID
	delay := asyncProbeCallbackBaseDelay
	for attempt := 1; attempt <= asyncProbeCallbackMaxAttempts; attempt++ {
		resp, err := asyncProbeHTTPClient.Post(url, "application/json", bytes.NewReader(body))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
		}
		if attempt == asyncProbeCallbackMaxAttempts {
			return
		}
		time.Sleep(delay)
		delay *= 2
	}
}

const defaultParkProbeScratch = "stub-park-scratch"

func parkProbeScratch(incoming []byte) []byte {
	if len(incoming) > 0 {
		return append([]byte(nil), incoming...)
	}
	return []byte(defaultParkProbeScratch)
}

func postCancelProbeSignal(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, callbackURL+"/v1/callback/"+ackID,
		strings.NewReader(`{"success":{"changed":false}}`))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cancelProbeHTTPClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func parkResumeAtFromAttrs(attrs map[string]any) *timestamppb.Timestamp {
	raw, _ := attrs[stubmode.ParkResumeAtAttribute].(string)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil
	}
	return timestamppb.New(t)
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
	parkResumeAt      time.Time
	tags              []string
	attributesDelta   map[string]any
	holdUntil         <-chan struct{}
}

type Stub struct {
	genv1.UnimplementedExecutorServer
	mu                sync.Mutex
	scripts           map[string][]*script
	callN             map[string]int
	stubMode          bool
	ignoreCancelProbe bool
	observed          []ObservedRequest
}

type ObservedRequest struct {
	NodeID                   string
	InstanceID               string
	NodeType                 string
	Attributes               map[string]any
	CallbackURL              string
	CancelToken              string
	NodeRunID                string
	PriorNodeRunID           string
	PriorDispatchDisposition genv1.PriorDispatchDisposition
	CandidateHandles         map[string][]byte
}

func New() *Stub {
	return &Stub{
		scripts: map[string][]*script{},
		callN:   map[string]int{},
	}
}

func (s *Stub) EnableStubMode() *Stub {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stubMode = true
	return s
}

func (s *Stub) IgnoreCancelProbe() *Stub {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ignoreCancelProbe = true
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
	s.scripts[t] = []*script{sc}
	s.callN[t] = 0
	return &TypeBuilder{s: s, typ: t}
}

func (b *TypeBuilder) tail() *script {
	q := b.s.scripts[b.typ]
	return q[len(q)-1]
}

func (b *TypeBuilder) Then() *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ] = append(b.s.scripts[b.typ], &script{terminal: termSuccess, changed: true})
	return b
}

func (b *TypeBuilder) Success(result any, changed bool, changeSummary string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.tail()
	sc.terminal, sc.result, sc.changed, sc.changeSum = termSuccess, result, changed, changeSummary
	return b
}

func (b *TypeBuilder) Error(class string, payload any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.tail()
	sc.terminal, sc.errorClass, sc.payload = termError, class, payload
	return b
}

func (b *TypeBuilder) AwaitAsyncCallback(ackID string, completionMs int64) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.tail()
	sc.terminal, sc.asyncAckID, sc.asyncCompletionMs = termAsync, ackID, completionMs
	return b
}

func (b *TypeBuilder) Park(resumeAt time.Time) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.tail()
	sc.terminal = termPark
	sc.parkResumeAt = resumeAt
	return b
}

func (b *TypeBuilder) Tags(tags ...string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.tail().tags = append([]string(nil), tags...)
	return b
}

func (b *TypeBuilder) AttributesDelta(delta map[string]any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	cp := make(map[string]any, len(delta))
	for k, v := range delta {
		cp[k] = v
	}
	b.tail().attributesDelta = cp
	return b
}

func (b *TypeBuilder) HoldUntil(ch <-chan struct{}) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.tail().holdUntil = ch
	return b
}

func (b *TypeBuilder) Delay(d time.Duration) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.tail().delay = d
	return b
}

func (s *Stub) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	var candidateHandles map[string][]byte
	if len(req.GetClaimProducers()) > 0 {
		candidateHandles = make(map[string][]byte, len(req.GetClaimProducers()))
		for alias, sh := range req.GetClaimProducers() {
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
		NodeRunID:                req.GetDispatchId(),
		PriorNodeRunID:           req.GetPriorDispatchId(),
		PriorDispatchDisposition: req.GetPriorDispatchDisposition(),
		CandidateHandles:         candidateHandles,
	})
	stubMode := s.stubMode
	ignoreCancelProbe := s.ignoreCancelProbe
	q, ok := s.scripts[req.NodeType]
	var sc *script
	if ok && len(q) > 0 {
		n := s.callN[req.NodeType]
		idx := n
		if idx >= len(q) {
			idx = len(q) - 1
		}
		sc = q[idx]
		s.callN[req.NodeType] = n + 1
	} else {
		ok = false
	}
	s.mu.Unlock()

	if stubMode {
		attrs := req.GetAttributes().AsMap()
		if stubmode.IsCancelProbe(attrs) && !ignoreCancelProbe {
			postCancelProbeSignal(req.GetCallbackUrl(), "cancel-observed")
			<-ctx.Done()
			postCancelProbeSignal(req.GetCallbackUrl(), "cancel-acknowledged")
			return nil, ctx.Err()
		}
		if stubmode.IsParkProbe(attrs) {
			park := &genv1.Park{
				ResumeAt: parkResumeAtFromAttrs(attrs),
				Scratch:  parkProbeScratch(req.GetScratch()),
			}
			return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: park}}, nil
		}
		if stubmode.IsAsyncProbe(attrs) {
			ackID := "stub-async-" + uuid.NewString()
			go postAsyncProbeCallback(req.GetCallbackUrl(), ackID)
			return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
				AsyncAckId: ackID,
			}}}, nil
		}
		if stubmode.IsProbe(attrs) {
			delta, err := structpb.NewStruct(stubmode.ResponseDelta())
			if err != nil {
				return nil, err
			}
			return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
				AttributesDelta: delta,
				Changed:         true,
				ChangeSummary:   "stub",
			}}}, nil
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
		deltaSrc := sc.result
		if sc.attributesDelta != nil {
			deltaSrc = sc.attributesDelta
		}
		delta, err := toStruct(deltaSrc)
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
		errOut := &genv1.Error{
			ErrorClass: prefixedStubClass(sc.errorClass),
			Payload:    v,
			Tags:       sc.tags,
		}
		if sc.attributesDelta != nil {
			delta, derr := structpb.NewStruct(sc.attributesDelta)
			if derr != nil {
				return nil, derr
			}
			errOut.AttributesDelta = delta
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: errOut}}, nil
	case termAsync:
		return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId:           sc.asyncAckID,
			ExpectedCompletionMs: sc.asyncCompletionMs,
		}}}, nil
	case termPark:
		park := &genv1.Park{
			Tags: sc.tags,
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
