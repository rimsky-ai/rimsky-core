// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakeStream struct {
	events []*genv1.ExecuteEvent
	idx    int
	closed bool
}

func (f *fakeStream) Recv() (*genv1.ExecuteEvent, error) {
	if f.idx >= len(f.events) {
		return nil, io.EOF
	}
	ev := f.events[f.idx]
	f.idx++
	return ev, nil
}
func (f *fakeStream) Close() error { f.closed = true; return nil }

var _ EventStream = (*fakeStream)(nil)

// successEvent builds an ExecuteEvent with a StreamClose{Success} terminal.
func successEvent(changed bool, summary string) *genv1.ExecuteEvent {
	return &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			Changed: changed, ChangeSummary: summary,
		}}},
	}}
}

// awaitAsyncEvent builds an ExecuteEvent with a StreamClose{AwaitAsync} terminal.
func awaitAsyncEvent(ackID string) *genv1.ExecuteEvent {
	return &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId: ackID,
		}}},
	}}
}

func TestAwaitTerminal_SyncTerminalReturnedDirectly(t *testing.T) {
	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{}}},
		successEvent(true, ""),
	}}
	env := Env{}
	ev, err := AwaitTerminal(context.Background(), stream, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		t.Fatalf("expected StreamClose, got %T", ev.Event)
	}
	succ, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_Success)
	if !ok {
		t.Fatalf("expected Success outcome, got %T", sc.StreamClose.Outcome)
	}
	if !succ.Success.Changed {
		t.Errorf("changed not propagated")
	}
}

func TestAwaitTerminal_AsyncAccepted_NoCallbacksReturnsAsIs(t *testing.T) {
	// @deliberate: Env.Callbacks == nil → AwaitAsyncCallback is returned
	// as-is rather than being followed.
	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		awaitAsyncEvent("ack-x"),
	}}
	env := Env{Callbacks: nil}
	ev, err := AwaitTerminal(context.Background(), stream, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		t.Fatalf("expected StreamClose, got %T", ev.Event)
	}
	if _, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_AwaitAsync); !ok {
		t.Fatalf("expected AwaitAsync outcome, got %T", sc.StreamClose.Outcome)
	}
}

func TestAwaitTerminal_AsyncAccepted_FollowsCallbackToTerminal(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-follow"
	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		awaitAsyncEvent(ackID),
	}}
	env := Env{Callbacks: r}

	// @deliberate: POST the synthesized terminal in the background once
	// AwaitTerminal has started waiting on the receiver channel.
	go func() {
		time.Sleep(50 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{
			"success": map[string]any{"changed": true, "change_summary": "ok"},
		})
		resp, err := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Errorf("POST: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ev, err := AwaitTerminal(ctx, stream, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		t.Fatalf("expected synthesized StreamClose from callback, got %T", ev.Event)
	}
	succ, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_Success)
	if !ok {
		t.Fatalf("expected Success outcome, got %T", sc.StreamClose.Outcome)
	}
	if !succ.Success.Changed {
		t.Errorf("changed not propagated through synthesized event")
	}
}

func TestAwaitTerminal_AsyncAccepted_PreRegisteredCallbackArrivesEarly(t *testing.T) {
	// @deliberate: cover the race window where the executor's callback POST
	// arrives BEFORE AwaitTerminal extracts the ack_id and calls Register —
	// the receiver's handle() pre-creates the channel and buffers the event;
	// Register returns the same channel and the event delivers immediately.
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-early"
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{"error_class": "boom"},
	})
	resp, err := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		awaitAsyncEvent(ackID),
	}}
	env := Env{Callbacks: r}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, err := AwaitTerminal(ctx, stream, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		t.Fatalf("expected StreamClose, got %T", ev.Event)
	}
	e, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_Error)
	if !ok || e.Error.ErrorClass != "boom" {
		t.Fatalf("expected Error.boom synthesized from buffered callback, got %T", sc.StreamClose.Outcome)
	}
}

func TestAwaitTerminal_ContextCancelledWhileAwaiting(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		awaitAsyncEvent("ack-never"),
	}}
	env := Env{Callbacks: r}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = AwaitTerminal(ctx, stream, env)
	if err == nil {
		t.Fatal("expected error when callback never arrives, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	if !strings.Contains(err.Error(), "ack-never") {
		t.Errorf("expected error to mention ack id, got %v", err)
	}
}

func TestAwaitTerminal_AsyncAcceptedEmptyAckIDIsError(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		awaitAsyncEvent(""),
	}}
	env := Env{Callbacks: r}
	if _, err := AwaitTerminal(context.Background(), stream, env); err == nil {
		t.Fatal("expected error for empty ack_id with callbacks configured")
	}
}

func TestAwaitTerminal_StreamEndsWithoutTerminal(t *testing.T) {
	stream := &fakeStream{events: []*genv1.ExecuteEvent{
		{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{}}},
	}}
	env := Env{}
	if _, err := AwaitTerminal(context.Background(), stream, env); err == nil {
		t.Fatal("expected error when stream ends without terminal")
	}
}
