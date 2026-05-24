// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPayloads_RoundTrip exercises json.Marshal + json.Unmarshal on
// every payload struct, confirming fields survive the round-trip.
func TestPayloads_RoundTrip(t *testing.T) {
	t.Run("TerminalSuccessPayload", func(t *testing.T) {
		in := TerminalSuccessPayload{
			Changed:         true,
			AttributesDelta: map[string]any{"x": "y"},
			ChangeSummary:   "foo",
		}
		var out TerminalSuccessPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TerminalErrorPayload", func(t *testing.T) {
		in := TerminalErrorPayload{
			ErrorClass:   "http/timeout",
			ErrorPayload: map[string]any{"status": float64(504)},
			Attempt:      2,
			RetriesSoFar: 1,
		}
		var out TerminalErrorPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TerminalParkSnoozePayload", func(t *testing.T) {
		in := TerminalParkSnoozePayload{
			ResumeAt:          time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
			SessionToken:      "tok",
			ParkPayload:       []byte("hi"),
			ParkedReasonLabel: "wait",
			ParkedReasonNote:  "ten min",
		}
		var out TerminalParkSnoozePayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TerminalParkAwaitCallbackPayload", func(t *testing.T) {
		ts := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
		in := TerminalParkAwaitCallbackPayload{
			ResumeAt:          &ts,
			SessionToken:      "tok",
			ParkPayload:       []byte("hi"),
			ParkedReasonLabel: "wait",
			ParkedReasonNote:  "callback",
		}
		var out TerminalParkAwaitCallbackPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TerminalInfraPayload", func(t *testing.T) {
		in := TerminalInfraPayload{
			Reason:  "heartbeat_lost",
			Details: map[string]any{"k": "v"},
		}
		var out TerminalInfraPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TransientRetryPayload", func(t *testing.T) {
		in := TransientRetryPayload{
			Attempt:         3,
			Cap:             5,
			ErrorClass:      "agent/rate_limited",
			DiscardedClaims: true,
			DelayMs:         500,
			ErrorPayload:    map[string]any{"k": "v"},
		}
		var out TransientRetryPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TransientHeartbeatMissedPayload", func(t *testing.T) {
		in := TransientHeartbeatMissedPayload{
			LastHeartbeatAt: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
			DispatchID:      uuid.New(),
			ThresholdMs:     30000,
		}
		var out TransientHeartbeatMissedPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("TransientAwaitAsyncPayload", func(t *testing.T) {
		in := TransientAwaitAsyncPayload{
			AsyncAckID:  "ack-1",
			CallbackURL: "https://supervisor/callback",
		}
		var out TransientAwaitAsyncPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("AttributeChangedPayload", func(t *testing.T) {
		in := AttributeChangedPayload{
			Key:      "budget_cents",
			Value:    float64(100),
			OldValue: float64(50),
		}
		var out AttributeChangedPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("EventPayload", func(t *testing.T) {
		in := EventPayload{
			Name:         "discovered",
			EventPayload: map[string]any{"k": "v"},
		}
		var out EventPayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})

	t.Run("MessagePayload", func(t *testing.T) {
		in := MessagePayload{
			Kind:           "invalidate",
			SenderKind:     "operator",
			Sender:         "alice",
			Target:         "self",
			MessagePayload: map[string]any{"k": "v"},
		}
		var out MessagePayload
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round-trip mismatch: in=%+v out=%+v", in, out)
		}
	})
}

func roundTrip(t *testing.T, in any, out any) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestPayloadSchemaForType(t *testing.T) {
	cases := []struct {
		path       TypePath
		want       reflect.Type
		wantSecond bool
	}{
		{"terminal/success", reflect.TypeOf(TerminalSuccessPayload{}), true},
		{"terminal/error/http/timeout", reflect.TypeOf(TerminalErrorPayload{}), true},
		{"terminal/error/foo", reflect.TypeOf(TerminalErrorPayload{}), true},
		{"terminal/park/snooze", reflect.TypeOf(TerminalParkSnoozePayload{}), true},
		{"terminal/park/await_callback", reflect.TypeOf(TerminalParkAwaitCallbackPayload{}), true},
		{"terminal/infra/heartbeat_lost", reflect.TypeOf(TerminalInfraPayload{}), true},
		{"transient/retry/3/agent/rate_limited", reflect.TypeOf(TransientRetryPayload{}), true},
		{"transient/heartbeat_missed", reflect.TypeOf(TransientHeartbeatMissedPayload{}), true},
		{"transient/await_async", reflect.TypeOf(TransientAwaitAsyncPayload{}), true},
		{"attribute/budget_cents/changed", reflect.TypeOf(AttributeChangedPayload{}), true},
		{"event/discovered", reflect.TypeOf(EventPayload{}), true},
		{"message/invalidate/operator/self", reflect.TypeOf(MessagePayload{}), true},
		// Prefix paths return (nil, false).
		{"terminal/*", nil, false},
		{"terminal/error/*", nil, false},
		{"attribute/*", nil, false},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.path), func(t *testing.T) {
			got, ok := PayloadSchemaForType(c.path)
			if ok != c.wantSecond {
				t.Fatalf("PayloadSchemaForType(%q): ok=%v; want=%v", c.path, ok, c.wantSecond)
			}
			if got != c.want {
				t.Fatalf("PayloadSchemaForType(%q): got=%v want=%v", c.path, got, c.want)
			}
		})
	}
}
