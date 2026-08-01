// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestReadExecutorOutcome_NilOutcome(t *testing.T) {
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, nil)
	if term.Kind != terminalKindInfra {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindInfra)
	}
	if term.ErrorClass != "executor_returned_nil_outcome" {
		t.Fatalf("ErrorClass = %q, want %q", term.ErrorClass, "executor_returned_nil_outcome")
	}
	if ack != "" {
		t.Fatalf("ack = %q, want empty", ack)
	}
}

func TestReadExecutorOutcome_UnknownOutcome(t *testing.T) {
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, &genv1.Outcome{})
	if term.Kind != terminalKindInfra {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindInfra)
	}
	if term.ErrorClass != "executor_returned_unknown_outcome" {
		t.Fatalf("ErrorClass = %q, want %q", term.ErrorClass, "executor_returned_unknown_outcome")
	}
	if ack != "" {
		t.Fatalf("ack = %q, want empty", ack)
	}
}

func TestReadExecutorOutcome_Success(t *testing.T) {
	outcome := &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       true,
		ChangeSummary: "did the thing",
	}}}
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, outcome)
	if term.Kind != terminalKindComplete {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindComplete)
	}
	if !term.Changed || term.ChangeSummary != "did the thing" {
		t.Fatalf("Changed/ChangeSummary = %v/%q, want true/\"did the thing\"", term.Changed, term.ChangeSummary)
	}
	if ack != "" {
		t.Fatalf("ack = %q, want empty", ack)
	}
}

func TestReadExecutorOutcome_Error(t *testing.T) {
	outcome := &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: "boom_class",
	}}}
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, outcome)
	if term.Kind != terminalKindErrored {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindErrored)
	}
	if term.ErrorClass != "boom_class" {
		t.Fatalf("ErrorClass = %q, want %q", term.ErrorClass, "boom_class")
	}
	if ack != "" {
		t.Fatalf("ack = %q, want empty", ack)
	}
}

func TestReadExecutorOutcome_ParkMissingResumeAtIsProtocolViolation(t *testing.T) {
	outcome := &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{}}}
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, outcome)
	if term.Kind != terminalKindErrored {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindErrored)
	}
	if term.ErrorClass != "executor_protocol_violation" {
		t.Fatalf("ErrorClass = %q, want %q", term.ErrorClass, "executor_protocol_violation")
	}
	if ack != "" {
		t.Fatalf("ack = %q, want empty", ack)
	}
}

func TestReadExecutorOutcome_ParkWithResumeAt(t *testing.T) {
	resumeAt := time.Now().Add(time.Hour).UTC()
	outcome := &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
		ResumeAt: timestamppb.New(resumeAt),
	}}}
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, outcome)
	if term.Kind != terminalKindPark {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindPark)
	}
	if !term.ParkResumeAt.Equal(resumeAt) {
		t.Fatalf("ParkResumeAt = %v, want %v", term.ParkResumeAt, resumeAt)
	}
	if ack != "" {
		t.Fatalf("ack = %q, want empty", ack)
	}
}

func TestReadExecutorOutcome_AwaitAsync(t *testing.T) {
	outcome := &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
		AsyncAckId: "ack-123",
	}}}
	term, ack := readExecutorOutcome(context.Background(), dispatchContext{}, outcome)
	if term.Kind != terminalKindAsyncAccepted {
		t.Fatalf("Kind = %v, want %v", term.Kind, terminalKindAsyncAccepted)
	}
	if ack != "ack-123" {
		t.Fatalf("ack = %q, want %q", ack, "ack-123")
	}
}
