// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func successOutcome(changed bool, summary string) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed: changed, ChangeSummary: summary,
	}}}
}

func awaitAsyncOutcome(ackID string) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
		AsyncAckId: ackID,
	}}}
}

func errorOutcome(class string) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class,
	}}}
}

func parkOutcome(resumeAt time.Time) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
		ResumeAt: timestamppb.New(resumeAt),
	}}}
}

func waitForAckRegistration(t *testing.T, r *CallbackReceiver, ackID string) {
	t.Helper()
	pollUntil(t, "AwaitTerminal to register a waiter for ack id "+ackID, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		_, registered := r.wait[ackID]
		return registered
	})
}

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

func TestAwaitTerminal_SyncParkReturnedDirectly(t *testing.T) {
	out := parkOutcome(time.Now().Add(time.Hour))
	got, err := AwaitTerminal(context.Background(), out, Env{})
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	park, ok := got.GetOutcome().(*genv1.Outcome_Park)
	if !ok {
		t.Fatalf("expected Park, got %T", got.GetOutcome())
	}
	if park.Park.GetResumeAt() == nil {
		t.Errorf("park resume_at should be preserved")
	}
}

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
		waitForAckRegistration(t, r, ackID)
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

	ctx := context.Background()
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

func TestAwaitTerminal_UnregistersAckIDAfterDelivery(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-cleanup"
	out := awaitAsyncOutcome(ackID)
	env := Env{Callbacks: r}

	go func() {
		waitForAckRegistration(t, r, ackID)
		body, _ := json.Marshal(map[string]any{
			"success": map[string]any{"changed": true},
		})
		resp, perr := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if perr != nil {
			t.Errorf("POST: %v", perr)
			return
		}
		_ = resp.Body.Close()
	}()

	ctx := context.Background()
	if _, err := AwaitTerminal(ctx, out, env); err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}

	r.mu.Lock()
	_, stillWaiting := r.wait[ackID]
	r.mu.Unlock()
	if stillWaiting {
		t.Fatal("expected wait map entry to be removed after AwaitTerminal delivered the outcome")
	}
}

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
		waitForAckRegistration(t, r, ackID)
		body, _ := json.Marshal(map[string]any{
			"park": map[string]any{
				"resume_at": resumeAt,
			},
		})
		resp, perr := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if perr != nil {
			t.Errorf("POST: %v", perr)
			return
		}
		_ = resp.Body.Close()
	}()

	ctx := context.Background()
	got, err := AwaitTerminal(ctx, out, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	park, ok := got.GetOutcome().(*genv1.Outcome_Park)
	if !ok {
		t.Fatalf("expected Park from callback, got %T", got.GetOutcome())
	}
	if park.Park.GetResumeAt() == nil {
		t.Errorf("park resume_at should be propagated from the callback body")
	}
}

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
		waitForAckRegistration(t, r, ackID)
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

	ctx := context.Background()
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

	got, err := AwaitTerminal(context.Background(), out, env)
	if err != nil {
		t.Fatalf("AwaitTerminal: %v", err)
	}
	errOut, ok := got.GetOutcome().(*genv1.Outcome_Error)
	if !ok || errOut.Error.GetErrorClass() != "boom" {
		t.Fatalf("expected buffered Error.boom, got %T", got.GetOutcome())
	}
}

func TestAwaitTerminal_ContextCancelledWhileAwaiting(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	out := awaitAsyncOutcome("ack-never")
	env := Env{Callbacks: r}

	ctx, cancel := context.WithCancel(context.Background())
	awaiting := make(chan error, 1)
	go func() {
		_, awaitErr := AwaitTerminal(ctx, out, env)
		awaiting <- awaitErr
	}()
	cancel()

	err = <-awaiting
	if err == nil {
		t.Fatal("expected error when the await is cancelled and the callback never arrives, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected Canceled, got %v", err)
	}
	if !strings.Contains(err.Error(), "ack-never") {
		t.Errorf("expected error to mention ack id, got %v", err)
	}
}

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

func TestAwaitTerminal_NilOutcomeIsError(t *testing.T) {
	_, err := AwaitTerminal(context.Background(), nil, Env{})
	if err == nil {
		t.Fatal("expected error for nil Outcome")
	}
}

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
