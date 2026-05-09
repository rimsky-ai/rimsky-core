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

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// CallbackReceiver is the conformance-side endpoint that async executors POST
// their terminal verdicts to. It listens on a kernel-allocated port and routes
// each incoming POST by `async_ack_id` to a per-scenario channel registered
// ahead of time via Register.
//
// Scenarios that test terminal verdicts use AwaitTerminal — it reads the gRPC
// stream until it sees a terminal event. If that terminal is AsyncAccepted,
// AwaitTerminal then waits on the receiver's channel for the eventual callback
// POST and synthesizes an equivalent ExecuteEvent for the caller. This lets
// the same scenario validate both synchronous and async executors without
// per-scenario branching.
//
// The receiver accepts both the post-Plan-A5 AsyncCallbackBody shape
// (`{events?, complete|blocked|errored|park_requested}`) and the legacy
// shape (`{type: "complete"|"blocked"|"errored", ...}`) — same as the
// supervisor's parser at foundation/integration/callback.go.
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

// parseCallbackBody accepts both the new AsyncCallbackBody shape and the legacy
// `{type: "..."}` shape. Returns a synthesized ExecuteEvent with the terminal
// oneof populated.
//
// Mirrors the supervisor's parser at foundation/integration/callback.go's
// tryParseAsyncCallback: when the new-shape body declares more than one
// terminal field (complete | blocked | errored | park_requested), reject
// outright. The conformance suite must surface this defect because the
// supervisor would explode on it in production.
func parseCallbackBody(body map[string]any) (*genv1.ExecuteEvent, error) {
	// New shape: top-level keys complete | blocked | errored | park_requested.
	terminalCount := 0
	if _, ok := body["complete"]; ok {
		terminalCount++
	}
	if _, ok := body["blocked"]; ok {
		terminalCount++
	}
	if _, ok := body["errored"]; ok {
		terminalCount++
	}
	if _, ok := body["park_requested"]; ok {
		terminalCount++
	}
	if terminalCount > 1 {
		return nil, fmt.Errorf("async callback body must include exactly one terminal field; got %d", terminalCount)
	}
	if v, ok := body["complete"]; ok {
		return mapComplete(asMap(v))
	}
	if v, ok := body["blocked"]; ok {
		return mapBlocked(asMap(v))
	}
	if v, ok := body["errored"]; ok {
		return mapErrored(asMap(v))
	}
	if v, ok := body["park_requested"]; ok {
		return mapParkRequested(asMap(v))
	}
	// Legacy shape: top-level "type".
	t, _ := body["type"].(string)
	switch t {
	case "complete":
		return mapComplete(body)
	case "blocked":
		return mapBlocked(body)
	case "errored":
		return mapErrored(body)
	}
	return nil, fmt.Errorf("callback body has no terminal field")
}

func mapComplete(m map[string]any) (*genv1.ExecuteEvent, error) {
	delta, _ := structpb.NewStruct(asMap(m["attributes_delta"]))
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_Complete{
			Complete: &genv1.Complete{
				AttributesDelta: delta,
				Changed:         asBool(m["changed"]),
				ChangeSummary:   asString(m["change_summary"]),
			},
		},
	}, nil
}

func mapBlocked(m map[string]any) (*genv1.ExecuteEvent, error) {
	// Blocked carries a `context` Struct on the wire, not `payload`. Tolerate
	// both keys to ease writing test fixtures.
	ctx, _ := structpb.NewStruct(asMap(m["context"]))
	if ctx == nil {
		ctx, _ = structpb.NewStruct(asMap(m["payload"]))
	}
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_Blocked{
			Blocked: &genv1.Blocked{
				Reason:  asString(m["reason"]),
				Context: ctx,
			},
		},
	}, nil
}

func mapErrored(m map[string]any) (*genv1.ExecuteEvent, error) {
	pl, _ := structpb.NewStruct(asMap(m["payload"]))
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_Errored{
			Errored: &genv1.Errored{
				ErrorClass: asString(m["error_class"]),
				Payload:    pl,
			},
		},
	}, nil
}

func mapParkRequested(m map[string]any) (*genv1.ExecuteEvent, error) {
	payloadStr := asString(m["payload"])
	var payloadBytes []byte
	if payloadStr != "" {
		// Per Plan A5 the payload is base64 over the JSON wire (Go []byte
		// convention). Tolerate non-base64 by treating it as a literal.
		if decoded, err := base64.StdEncoding.DecodeString(payloadStr); err == nil {
			payloadBytes = decoded
		} else {
			payloadBytes = []byte(payloadStr)
		}
	}
	pr := &genv1.ParkRequested{
		Reason:       asString(m["reason"]),
		Payload:      payloadBytes,
		SessionToken: asString(m["session_token"]),
	}
	// resume_at is RFC3339 over the JSON wire (matches the TS executor's
	// outcomeToCallbackBody emit shape and the supervisor's
	// tryParseAsyncCallback parser at foundation/integration/callback.go).
	// Tolerate absence; keep ResumeAt nil when the executor didn't send one.
	if rawResume := asString(m["resume_at"]); rawResume != "" {
		if pt, err := time.Parse(time.RFC3339, rawResume); err == nil {
			pr.ResumeAt = timestamppb.New(pt)
		}
	}
	return &genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_ParkRequested{
			ParkRequested: pr,
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
