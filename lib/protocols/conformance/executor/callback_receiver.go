// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type CallbackReceiver struct {
	srv            *http.Server
	bindAddr       string
	advertiseURL   string
	mu             sync.Mutex
	wait           map[string]chan *genv1.Outcome
	delivered      map[string]bool
	restartSim     bool
	restartSimHits chan string
	attemptTimes   map[string][]time.Time
}

type ReceiverOptions struct {
	BindHost      string
	AdvertiseHost string
}

func StartCallbackReceiver(opts ...ReceiverOptions) (*CallbackReceiver, error) {
	o := ReceiverOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.BindHost == "" {
		o.BindHost = "127.0.0.1"
	}
	advertise := o.AdvertiseHost
	if advertise == "" || advertise == "0.0.0.0" {
		advertise = o.BindHost
		if advertise == "0.0.0.0" {
			advertise = "127.0.0.1"
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(o.BindHost, "0"))
	if err != nil {
		return nil, fmt.Errorf("callback receiver listen: %w", err)
	}
	tcpAddr, _ := ln.Addr().(*net.TCPAddr)
	r := &CallbackReceiver{
		bindAddr:     ln.Addr().String(),
		advertiseURL: fmt.Sprintf("http://%s:%d", advertise, tcpAddr.Port),
		wait:         map[string]chan *genv1.Outcome{},
		delivered:    map[string]bool{},
		attemptTimes: map[string][]time.Time{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/callback/", r.handle)
	r.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.srv.Serve(ln) }()
	return r, nil
}

func (r *CallbackReceiver) URL() string {
	if r == nil {
		return ""
	}
	return r.advertiseURL
}

func (r *CallbackReceiver) Register(ackID string) <-chan *genv1.Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.wait[ackID]
	if !ok {
		ch = make(chan *genv1.Outcome, 1)
		r.wait[ackID] = ch
	}
	return ch
}

func (r *CallbackReceiver) Unregister(ackID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.wait, ackID)
}

func (r *CallbackReceiver) SimulateRestart() <-chan string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restartSim = true
	r.restartSimHits = make(chan string, 32)
	return r.restartSimHits
}

// @concept: executor
func (r *CallbackReceiver) AttemptTimes(ackID string) []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Time, len(r.attemptTimes[ackID]))
	copy(out, r.attemptTimes[ackID])
	return out
}

func (r *CallbackReceiver) EndSimulatedRestart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restartSim = false
}

func (r *CallbackReceiver) Close() error {
	if r == nil || r.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.srv.Shutdown(ctx)
}

func (r *CallbackReceiver) handle(w http.ResponseWriter, req *http.Request) {
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v1/callback/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing async_ack_id", http.StatusBadRequest)
		return
	}
	ackID := parts[0]

	r.mu.Lock()
	simulating := r.restartSim
	hits := r.restartSimHits
	r.attemptTimes[ackID] = append(r.attemptTimes[ackID], time.Now())
	r.mu.Unlock()
	if simulating {
		select {
		case hits <- ackID:
		default:
		}
		http.Error(w, "simulated supervisor restart", http.StatusServiceUnavailable)
		return
	}
	body := map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	outcome, err := parseCallbackBody(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	alreadyDelivered := r.delivered[ackID]
	r.delivered[ackID] = true
	ch, ok := r.wait[ackID]
	if !ok {
		ch = make(chan *genv1.Outcome, 1)
		r.wait[ackID] = ch
	}
	r.mu.Unlock()

	if alreadyDelivered {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ack_status": ackStatusRejectedRunTerminal})
		return
	}

	select {
	case ch <- outcome:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

const ackStatusRejectedRunTerminal = "rejected_run_terminal"

func parseCallbackBody(body map[string]any) (*genv1.Outcome, error) {
	for k := range body {
		switch k {
		case "success", "error", "park":
		default:
			return nil, fmt.Errorf("unknown top-level field %q; AsyncCallbackBody carries exactly one of success | error | park (events is reserved and no longer accepted)", k)
		}
	}
	outcomeCount := 0
	if v, ok := body["success"]; ok && v != nil {
		outcomeCount++
	}
	if v, ok := body["error"]; ok && v != nil {
		outcomeCount++
	}
	if v, ok := body["park"]; ok && v != nil {
		outcomeCount++
	}
	if outcomeCount != 1 {
		return nil, fmt.Errorf("expected AsyncCallbackBody; outcome oneof must be set to a non-null value (success | error | park); got %d outcomes", outcomeCount)
	}
	if v, ok := body["success"]; ok && v != nil {
		return mapSuccess(asMap(v)), nil
	}
	if v, ok := body["error"]; ok && v != nil {
		return mapErrorOutcome(asMap(v)), nil
	}
	return mapPark(asMap(body["park"]))
}

func mapSuccess(m map[string]any) *genv1.Outcome {
	delta, _ := structpb.NewStruct(asMap(m["attributes_delta"]))
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         asBool(m["changed"]),
			ChangeSummary:   asString(m["change_summary"]),
			Tags:            asStringSlice(m["tags"]),
			Scratch:         asBytes(m["scratch"]),
		}},
	}
}

func mapErrorOutcome(m map[string]any) *genv1.Outcome {
	pl, _ := structpb.NewStruct(asMap(m["payload"]))
	delta, _ := structpb.NewStruct(asMap(m["attributes_delta"]))
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass:      asString(m["error_class"]),
			Payload:         pl,
			AttributesDelta: delta,
			Tags:            asStringSlice(m["tags"]),
			Scratch:         asBytes(m["scratch"]),
		}},
	}
}

func mapPark(m map[string]any) (*genv1.Outcome, error) {
	rawResume := asString(m["resume_at"])
	if rawResume == "" {
		return nil, fmt.Errorf("park.resume_at is required; a Park without resume_at is rejected as a protocol violation")
	}
	pt, err := time.Parse(time.RFC3339, rawResume)
	if err != nil {
		return nil, fmt.Errorf("park.resume_at: invalid RFC3339 timestamp %q: %w", rawResume, err)
	}
	p := &genv1.Park{
		ResumeAt: timestamppb.New(pt),
		Tags:     asStringSlice(m["tags"]),
		Scratch:  asBytes(m["scratch"]),
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: p}}, nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func asString(v any) string {
	s, _ := v.(string)
	return s
}
func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}
func asBytes(v any) []byte {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}
func asStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
