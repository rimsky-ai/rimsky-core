// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

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

	t.Run("TransientParkPayload", func(t *testing.T) {
		in := TransientParkPayload{
			ResumeAt: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
			Tags:     []string{"rate_limited"},
		}
		var out TransientParkPayload
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

	t.Run("TerminalSuccessPayloadWithTags", func(t *testing.T) {
		in := TerminalSuccessPayload{
			Changed:       true,
			ChangeSummary: "loop iteration",
			Tags:          []string{"loop", "iteration_3"},
		}
		var out TerminalSuccessPayload
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
		{"transient/park", reflect.TypeOf(TransientParkPayload{}), true},
		{"transient/park/snooze", nil, false},
		{"transient/park/await_callback", nil, false},
		{"terminal/infra/heartbeat_lost", nil, false},
		{"terminal/park/snooze", nil, false},
		{"transient/retry/3/agent/rate_limited", reflect.TypeOf(TransientRetryPayload{}), true},
		{"transient/await_async", reflect.TypeOf(TransientAwaitAsyncPayload{}), true},
		{"attribute/budget_cents/changed", reflect.TypeOf(AttributeChangedPayload{}), true},
		{"event/discovered", nil, false},
		{"message/invalidate/operator/self", nil, false},
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

func TestBuildTerminalErrorSignal_CoversAllSchemaKeys(t *testing.T) {
	sig := BuildTerminalErrorSignal(
		"http/timeout",
		map[string]any{"status": 504},
		3,
		2,
		map[string]any{"last_error": "boom"},
		[]string{"diag", "retryable"},
	)
	if sig.Type != "terminal/error/http/timeout" {
		t.Fatalf("type: got %q want terminal/error/http/timeout", sig.Type)
	}
	schemaKeys := []string{"error_class", "error_payload", "attempt", "retries_so_far", "attributes_delta", "tags"}
	for _, k := range schemaKeys {
		if _, ok := sig.Payload[k]; !ok {
			t.Fatalf("payload missing schema key %q; payload=%+v", k, sig.Payload)
		}
	}
	if sig.Payload["error_class"] != "http/timeout" {
		t.Errorf("error_class: got %v", sig.Payload["error_class"])
	}
	if sig.Payload["attempt"] != 3 {
		t.Errorf("attempt: got %v", sig.Payload["attempt"])
	}
	if sig.Payload["retries_so_far"] != 2 {
		t.Errorf("retries_so_far: got %v", sig.Payload["retries_so_far"])
	}
	if delta, ok := sig.Payload["attributes_delta"].(map[string]any); !ok || delta["last_error"] != "boom" {
		t.Errorf("attributes_delta: got %v", sig.Payload["attributes_delta"])
	}
	if tags, ok := sig.Payload["tags"].([]string); !ok || len(tags) != 2 {
		t.Errorf("tags: got %v", sig.Payload["tags"])
	}
}

func TestBuildTerminalErrorSignal_NilAttributesDeltaEmits_EmptyMap(t *testing.T) {
	sig := BuildTerminalErrorSignal("foo", nil, 0, 0, nil, nil)
	delta, ok := sig.Payload["attributes_delta"].(map[string]any)
	if !ok {
		t.Fatalf("attributes_delta missing or not map; payload=%+v", sig.Payload)
	}
	if len(delta) != 0 {
		t.Fatalf("attributes_delta should be empty when nil passed; got %+v", delta)
	}
}

func TestBuildTerminalSuccessSignal_CoversAllSchemaKeys(t *testing.T) {
	sig := BuildTerminalSuccessSignal(
		true,
		map[string]any{"k": "v"},
		"did the thing",
		[]string{"loop", "iteration_3"},
	)
	if sig.Type != "terminal/success" {
		t.Fatalf("type: got %q want terminal/success", sig.Type)
	}
	schemaKeys := []string{"changed", "attributes_delta", "change_summary", "tags"}
	for _, k := range schemaKeys {
		if _, ok := sig.Payload[k]; !ok {
			t.Fatalf("payload missing schema key %q; payload=%+v", k, sig.Payload)
		}
	}
	if sig.Payload["changed"] != true {
		t.Errorf("changed: got %v", sig.Payload["changed"])
	}
	if sig.Payload["change_summary"] != "did the thing" {
		t.Errorf("change_summary: got %v", sig.Payload["change_summary"])
	}
	if delta, ok := sig.Payload["attributes_delta"].(map[string]any); !ok || delta["k"] != "v" {
		t.Errorf("attributes_delta: got %v", sig.Payload["attributes_delta"])
	}
	if tags, ok := sig.Payload["tags"].([]string); !ok || len(tags) != 2 {
		t.Errorf("tags: got %v", sig.Payload["tags"])
	}
}

func TestBuildTerminalSuccessSignal_NilAttributesDeltaEmits_EmptyMap(t *testing.T) {
	sig := BuildTerminalSuccessSignal(false, nil, "", nil)
	delta, ok := sig.Payload["attributes_delta"].(map[string]any)
	if !ok {
		t.Fatalf("attributes_delta missing or not map; payload=%+v", sig.Payload)
	}
	if len(delta) != 0 {
		t.Fatalf("attributes_delta should be empty when nil passed; got %+v", delta)
	}
}
