// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// successOutcome builds a unary Outcome carrying a Success terminal.
func successOutcome(changed bool, summary string) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed: changed, ChangeSummary: summary,
	}}}
}

// awaitAsyncOutcome builds a unary Outcome carrying an AwaitAsyncCallback handoff.
func awaitAsyncOutcome(ackID string) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
		AsyncAckId: ackID,
	}}}
}

// errorOutcome builds a unary Outcome carrying an Error terminal.
func errorOutcome(class string) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class,
	}}}
}

// parkOutcome builds a unary Outcome carrying a Park terminal.
func parkOutcome(reason genv1.ParkReason) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
		Reason: reason,
	}}}
}

// TestAwaitTerminal_SyncSuccessReturnedDirectly pins that AwaitTerminal
// is a pass-through for already-settling Outcomes — Success, Error, and
// Park return verbatim, no callback wait.
func TestAwaitTerminal_SyncSuccessReturnedDirectly(t *testing.T) {
	out := successOutcome(true, "applied")
	got, err := AwaitTerminal(context.Background(), out, Env{})
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	succ, ok := got.GetOutcome().(*genv1.Outcome_Success)
	if !ok {
		t.Fatalf("expected Success, got %T", got.GetOutcome())
	}
	if !succ.Success.GetChanged() {
		t.Errorf("changed not propagated")
	}
	if succ.Success.GetChangeSummary() != "applied" {
		t.Errorf("change_summary=%q want=applied", succ.Success.GetChangeSummary())
	}
}

// TestAwaitTerminal_SyncErrorReturnedDirectly pins that an Error outcome
// settles synchronously without engaging the callback receiver.
func TestAwaitTerminal_SyncErrorReturnedDirectly(t *testing.T) {
	out := errorOutcome("boom")
	got, err := AwaitTerminal(context.Background(), out, Env{})
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	errOut, ok := got.GetOutcome().(*genv1.Outcome_Error)
	if !ok {
		t.Fatalf("expected Error, got %T", got.GetOutcome())
	}
	if errOut.Error.GetErrorClass() != "boom" {
		t.Errorf("error_class=%q want=boom", errOut.Error.GetErrorClass())
	}
}

// TestAwaitTerminal_SyncParkReturnedDirectly pins that a Park outcome
// settles synchronously without engaging the callback receiver.
func TestAwaitTerminal_SyncParkReturnedDirectly(t *testing.T) {
	out := parkOutcome(genv1.ParkReason_PARK_REASON_SNOOZE)
	got, err := AwaitTerminal(context.Background(), out, Env{})
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	park, ok := got.GetOutcome().(*genv1.Outcome_Park)
	if !ok {
		t.Fatalf("expected Park, got %T", got.GetOutcome())
	}
	if park.Park.GetReason() != genv1.ParkReason_PARK_REASON_SNOOZE {
		t.Errorf("park reason=%v want=SNOOZE", park.Park.GetReason())
	}
}

// TestAwaitTerminal_AsyncWithoutReceiverReturnedAsIs pins that
// AwaitAsyncCallback is returned verbatim when env.Callbacks is nil —
// the caller wants the handoff intent itself, not the eventual verdict.
func TestAwaitTerminal_AsyncWithoutReceiverReturnedAsIs(t *testing.T) {
	out := awaitAsyncOutcome("ack-x")
	got, err := AwaitTerminal(context.Background(), out, Env{Callbacks: nil})
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	if _, ok := got.GetOutcome().(*genv1.Outcome_AwaitAsync); !ok {
		t.Fatalf("expected AwaitAsync, got %T", got.GetOutcome())
	}
}

// TestAwaitTerminal_AsyncFollowsCallbackToSuccess exercises the full
// async round-trip: AwaitAsyncCallback registers an ack id, a later
// HTTP POST to /v1/callback/{ackId} delivers the settling Success, and
// AwaitTerminal returns it.
func TestAwaitTerminal_AsyncFollowsCallbackToSuccess(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-follow"
	out := awaitAsyncOutcome(ackID)
	env := Env{Callbacks: r}

	go func() {
		time.Sleep(50 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{
			"success": map[string]any{
				"changed":          true,
				"change_summary":   "applied",
				"attributes_delta": map[string]any{"k": "v"},
				"tags":             []string{"loop"},
			},
		})
		resp, perr := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if perr != nil {
			t.Errorf("POST: %v", perr)
			return
		}
		_ = resp.Body.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := AwaitTerminal(ctx, out, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	succ, ok := got.GetOutcome().(*genv1.Outcome_Success)
	if !ok {
		t.Fatalf("expected Success from callback, got %T", got.GetOutcome())
	}
	if !succ.Success.GetChanged() {
		t.Errorf("changed not propagated through callback")
	}
	delta := succ.Success.GetAttributesDelta().AsMap()
	if delta["k"] != "v" {
		t.Errorf("attributes_delta=%v missing k=v", delta)
	}
	if tags := succ.Success.GetTags(); len(tags) != 1 || tags[0] != "loop" {
		t.Errorf("tags=%v want [loop]", tags)
	}
}

// TestAwaitTerminal_AsyncFollowsCallbackToPark covers the rate-limit
// case: the executor returns AwaitAsyncCallback, then later POSTs a
// Park terminal carrying reason + attribute carry-forward, with no
// session_token on the wire (per TD-remove-resume-context).
func TestAwaitTerminal_AsyncFollowsCallbackToPark(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-park"
	out := awaitAsyncOutcome(ackID)
	env := Env{Callbacks: r}

	resumeAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	go func() {
		time.Sleep(50 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{
			"park": map[string]any{
				"reason":           "snooze",
				"resume_at":        resumeAt,
				"reason_note":      "rate-limited",
				"attributes_delta": map[string]any{"session_token": "tok-1"},
			},
		})
		resp, perr := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if perr != nil {
			t.Errorf("POST: %v", perr)
			return
		}
		_ = resp.Body.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := AwaitTerminal(ctx, out, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	park, ok := got.GetOutcome().(*genv1.Outcome_Park)
	if !ok {
		t.Fatalf("expected Park from callback, got %T", got.GetOutcome())
	}
	if park.Park.GetReason() != genv1.ParkReason_PARK_REASON_SNOOZE {
		t.Errorf("park reason=%v want=SNOOZE", park.Park.GetReason())
	}
	if park.Park.GetReasonNote() != "rate-limited" {
		t.Errorf("reason_note=%q", park.Park.GetReasonNote())
	}
	// @constraint: session state rides attributes_delta now
	// (per TD-remove-resume-context).
	delta := park.Park.GetAttributesDelta().AsMap()
	if delta["session_token"] != "tok-1" {
		t.Errorf("attributes_delta=%v missing session_token=tok-1", delta)
	}
}

// TestAwaitTerminal_AsyncFollowsCallbackToError pins error-routing via
// the async-callback round trip (the rate-limit case the stub scripts).
func TestAwaitTerminal_AsyncFollowsCallbackToError(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-error"
	out := awaitAsyncOutcome(ackID)
	env := Env{Callbacks: r}

	go func() {
		time.Sleep(50 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"error_class": "rate_limited",
				"payload":     map[string]any{"retry_after_s": 60},
			},
		})
		resp, perr := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if perr != nil {
			t.Errorf("POST: %v", perr)
			return
		}
		_ = resp.Body.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := AwaitTerminal(ctx, out, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	errOut, ok := got.GetOutcome().(*genv1.Outcome_Error)
	if !ok {
		t.Fatalf("expected Error from callback, got %T", got.GetOutcome())
	}
	if errOut.Error.GetErrorClass() != "rate_limited" {
		t.Errorf("error_class=%q", errOut.Error.GetErrorClass())
	}
}

// TestAwaitTerminal_AsyncPreRegisteredCallbackArrivesEarly covers the
// race window where the executor's callback POST arrives BEFORE
// AwaitTerminal calls Register — the receiver's handle() pre-creates
// the channel and buffers the outcome; Register returns the same
// channel and the outcome delivers immediately.
func TestAwaitTerminal_AsyncPreRegisteredCallbackArrivesEarly(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-early"
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{"error_class": "boom"},
	})
	resp, perr := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
	if perr != nil {
		t.Fatalf("POST: %v", perr)
	}
	_ = resp.Body.Close()

	out := awaitAsyncOutcome(ackID)
	env := Env{Callbacks: r}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := AwaitTerminal(ctx, out, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	errOut, ok := got.GetOutcome().(*genv1.Outcome_Error)
	if !ok || errOut.Error.GetErrorClass() != "boom" {
		t.Fatalf("expected buffered Error.boom, got %T", got.GetOutcome())
	}
}

// TestAwaitTerminal_ContextCancelledWhileAwaiting pins that a context
// deadline elapses cleanly when no callback arrives — the error names
// the ack id so the operator can correlate the timeout to the dispatch.
func TestAwaitTerminal_ContextCancelledWhileAwaiting(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	out := awaitAsyncOutcome("ack-never")
	env := Env{Callbacks: r}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = AwaitTerminal(ctx, out, env)
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

// TestAwaitTerminal_AsyncEmptyAckIDIsError pins that an
// AwaitAsyncCallback with no ack_id is rejected immediately — without
// an ack id there is no way to route the eventual callback.
func TestAwaitTerminal_AsyncEmptyAckIDIsError(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	out := awaitAsyncOutcome("")
	env := Env{Callbacks: r}
	if _, err := AwaitTerminal(context.Background(), out, env); err == nil {
		t.Fatal("expected error for empty ack_id with callbacks configured")
	}
}

// TestAwaitTerminal_NilOutcomeIsError pins that AwaitTerminal rejects
// a nil Outcome — defensive guard for the supervisor handing the
// runner a half-built response.
func TestAwaitTerminal_NilOutcomeIsError(t *testing.T) {
	_, err := AwaitTerminal(context.Background(), nil, Env{})
	if err == nil {
		t.Fatal("expected error for nil Outcome")
	}
}

// TestAwaitTerminal_TagsAndAttributesDeltaRoundTrip pins that the
// uniform settling-terminal shape (tags + attributes_delta per
// TD-collapse-named-event-to-tags + TD-attributes-delta-on-all-settling-
// terminals) round-trips verbatim on the sync Success path.
func TestAwaitTerminal_TagsAndAttributesDeltaRoundTrip(t *testing.T) {
	delta, _ := structpb.NewStruct(map[string]any{"count": 3, "session_token": "tok-z"})
	out := &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:         true,
		AttributesDelta: delta,
		Tags:            []string{"loop", "done"},
	}}}
	got, err := AwaitTerminal(context.Background(), out, Env{})
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	succ, ok := got.GetOutcome().(*genv1.Outcome_Success)
	if !ok {
		t.Fatalf("expected Success, got %T", got.GetOutcome())
	}
	gotDelta := succ.Success.GetAttributesDelta().AsMap()
	if gotDelta["session_token"] != "tok-z" {
		t.Errorf("session_token did not round-trip: %v", gotDelta)
	}
	if tags := succ.Success.GetTags(); len(tags) != 2 || tags[0] != "loop" || tags[1] != "done" {
		t.Errorf("tags=%v want [loop done]", tags)
	}
}
