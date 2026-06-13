// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

// CallbackReceiver is the conformance-side endpoint that async executors POST
// their terminal verdicts to. It listens on a kernel-allocated port and routes
// each incoming POST by `async_ack_id` to a per-scenario channel registered
// ahead of time via Register.
//
// Scenarios that test terminal verdicts use AwaitTerminal — it reads the gRPC
// stream until it sees a terminal StreamClose event. If that StreamClose
// outcome is AwaitAsyncCallback, AwaitTerminal then waits on the receiver's
// channel for the eventual callback POST and synthesizes an equivalent
// ExecuteEvent for the caller. This lets the same scenario validate both
// synchronous and async executors without per-scenario branching.
//
// The receiver accepts the AsyncCallbackBody shape with the outcome
// oneof keyed `success | error | park`. The pre-rename
// `{type: "complete"|"blocked"|"errored", ...}` discriminator shape and the
// per-terminal keys `complete | blocked | errored | park_requested` are no
// longer accepted — same as the supervisor's parser at
// runtime/callback.go.
type CallbackReceiver struct {
	srv          *http.Server
	bindAddr     string // listening "ip:port"
	advertiseURL string // base URL executors should POST to
	mu           sync.Mutex
	wait         map[string]chan *genv1.ExecuteEvent
}

// ReceiverOptions configures the address the receiver binds and the URL it
// advertises to executors. Both fields default sensibly for in-process /
// localhost use; containerized executors (e.g. claude-agent inside docker)
// need BindHost="0.0.0.0" and AdvertiseHost="host.docker.internal" or a
// reachable host IP so their callback POST can cross the network boundary.
type ReceiverOptions struct {
	BindHost      string // default "127.0.0.1"
	AdvertiseHost string // default same as BindHost; never "0.0.0.0"
}

// StartCallbackReceiver binds an HTTP listener and returns a CallbackReceiver
// ready to accept POSTs at `${URL()}/v1/callback/{async_ack_id}`. Caller must
// Close() to release the listener.
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
		wait:         map[string]chan *genv1.ExecuteEvent{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/callback/", r.handle)
	r.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.srv.Serve(ln) }()
	return r, nil
}

// URL returns the absolute base URL the receiver advertises, suitable for
// passing as `ExecuteRequest.callback_url` to the executor.
func (r *CallbackReceiver) URL() string {
	if r == nil {
		return ""
	}
	return r.advertiseURL
}

// Register reserves a channel for a future callback addressed to ackID. The
// returned channel receives exactly one ExecuteEvent (the synthesized terminal)
// and is then closed. Pre-registration eliminates the race where the executor's
// callback arrives before the scenario starts waiting for it.
func (r *CallbackReceiver) Register(ackID string) <-chan *genv1.ExecuteEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.wait[ackID]
	if !ok {
		ch = make(chan *genv1.ExecuteEvent, 1)
		r.wait[ackID] = ch
	}
	return ch
}

// Close stops the HTTP listener.
func (r *CallbackReceiver) Close() error {
	if r == nil || r.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.srv.Shutdown(ctx)
}

func (r *CallbackReceiver) handle(w http.ResponseWriter, req *http.Request) {
	// Path: /v1/callback/{async_ack_id}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v1/callback/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing async_ack_id", http.StatusBadRequest)
		return
	}
	ackID := parts[0]
	body := map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	ch, ok := r.wait[ackID]
	if !ok {
		ch = make(chan *genv1.ExecuteEvent, 1)
		r.wait[ackID] = ch
	}
	r.mu.Unlock()
	select {
	case ch <- ev:
	default:
		// Channel already has a buffered terminal; discard duplicates.
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseCallbackBody parses the AsyncCallbackBody shape. Top-level
// outcome oneof keys: success | error | park. The legacy
// `{type: ...}` discriminator shape and the legacy
// `complete | blocked | errored | park_requested` per-terminal keys
// are no longer accepted.
//
// @source: lib/runtime/callback.go::parseAsyncCallback
// @diverged: true
// @reason: The supervisor parses a typed body via json.Unmarshal into
// asyncCallbackBody. The conformance receiver operates on a
// map[string]any so test fixtures can be loose; payload bytes do not
// have to be base64-encoded here. Both must reject !=1 outcome bodies
// identically — that branch is the load-bearing parity check.
func parseCallbackBody(body map[string]any) (*genv1.ExecuteEvent, error) {
	outcomeCount := 0
	if _, ok := body["success"]; ok {
		outcomeCount++
	}
	if _, ok := body["error"]; ok {
		outcomeCount++
	}
	if _, ok := body["park"]; ok {
		outcomeCount++
	}
	if outcomeCount != 1 {
		return nil, fmt.Errorf("expected AsyncCallbackBody; outcome oneof must be set (success | error | park); got %d outcomes", outcomeCount)
	}
	if v, ok := body["success"]; ok {
		return mapSuccess(asMap(v))
	}
	if v, ok := body["error"]; ok {
		return mapErrorOutcome(asMap(v))
	}
	if v, ok := body["park"]; ok {
		return mapPark(asMap(v))
	}
	return nil, fmt.Errorf("callback body has no outcome field")
}

func mapSuccess(m map[string]any) (*genv1.ExecuteEvent, error) {
	delta, _ := structpb.NewStruct(asMap(m["attributes_delta"]))
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
				AttributesDelta: delta,
				Changed:         asBool(m["changed"]),
				ChangeSummary:   asString(m["change_summary"]),
			}}},
		},
	}, nil
}

func mapErrorOutcome(m map[string]any) (*genv1.ExecuteEvent, error) {
	pl, _ := structpb.NewStruct(asMap(m["payload"]))
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: asString(m["error_class"]),
				Payload:    pl,
			}}},
		},
	}, nil
}

func mapPark(m map[string]any) (*genv1.ExecuteEvent, error) {
	payloadStr := asString(m["payload"])
	var payloadBytes []byte
	if payloadStr != "" {
		// Payload is base64 over the JSON wire (Go []byte convention).
		// Tolerate non-base64 by treating it as a literal.
		if decoded, err := base64.StdEncoding.DecodeString(payloadStr); err == nil {
			payloadBytes = decoded
		} else {
			payloadBytes = []byte(payloadStr)
		}
	}
	// ParkReason is a closed two-value set (proto:executor.proto::ParkReason).
	// Unknown / empty falls back to PARK_REASON_AWAIT_CALLBACK — the safer
	// default (no auto-resume).
	reasonStr := asString(m["reason"])
	reasonEnum := genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK
	if reasonStr != "" {
		upper := "PARK_REASON_" + strings.ToUpper(reasonStr)
		if v, ok := genv1.ParkReason_value[upper]; ok {
			reasonEnum = genv1.ParkReason(v)
		}
	}
	p := &genv1.Park{
		Reason:       reasonEnum,
		ReasonNote:   asString(m["reason_note"]),
		Payload:      payloadBytes,
		SessionToken: asString(m["session_token"]),
	}
	if rawResume := asString(m["resume_at"]); rawResume != "" {
		if pt, err := time.Parse(time.RFC3339, rawResume); err == nil {
			p.ResumeAt = timestamppb.New(pt)
		}
	}
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Park{Park: p}},
		},
	}, nil
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
