// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestErrorPolicySignal_RetryShape(t *testing.T) {
	got := errorPolicySignal("foo", map[string]any{"k": "v"}, nil, nil, spec.ActionRetry, 1, 3, 500)
	if got.Type != signalpkg.TypePath("transient/retry/1/foo") {
		t.Fatalf("retry type: got %q want transient/retry/1/foo", got.Type)
	}
	if got.Payload.Map()["attempt"].(float64) != 1 {
		t.Fatalf("attempt: got %v want 1", got.Payload.Map()["attempt"])
	}
	if got.Payload.Map()["error_class"].(string) != "foo" {
		t.Fatalf("error_class: got %v want foo", got.Payload.Map()["error_class"])
	}
	if got.Payload.Map()["delay_ms"].(float64) != 500 {
		t.Fatalf("delay_ms: got %v want 500", got.Payload.Map()["delay_ms"])
	}
}

func TestErrorPolicySignal_ReleaseAndRequeueShape(t *testing.T) {
	got := errorPolicySignal("acquire/unavailable", nil, nil, nil, spec.ActionReleaseAndRequeue, 0, 3, 0)
	if got.Type != signalpkg.TypePath("transient/release_and_requeue/acquire/unavailable") {
		t.Fatalf("release_and_requeue type: got %q", got.Type)
	}
	if got.Payload.Map()["error_class"].(string) != "acquire/unavailable" {
		t.Fatalf("error_class: got %v", got.Payload.Map()["error_class"])
	}
}

func TestErrorPolicySignal_GiveUpShape(t *testing.T) {
	got := errorPolicySignal("http/timeout", map[string]any{"status": 504}, map[string]any{"retry_count": 3}, nil, spec.ActionGiveUp, 0, 3, 0)
	if got.Type != signalpkg.TypePath("terminal/error/http/timeout") {
		t.Fatalf("give_up type: got %q want terminal/error/http/timeout", got.Type)
	}
	if got.Payload.Map()["error_class"].(string) != "http/timeout" {
		t.Fatalf("error_class: got %v", got.Payload.Map()["error_class"])
	}
	delta, ok := got.Payload.Map()["attributes_delta"].(map[string]any)
	if !ok {
		t.Fatalf("attributes_delta missing or not map; payload=%+v", got.Payload)
	}
	if delta["retry_count"] != float64(3) {
		t.Fatalf("attributes_delta.retry_count: got %v want 3", delta["retry_count"])
	}
}

func TestErrorPolicySignal_GiveUpEmptyAttributesDelta(t *testing.T) {
	got := errorPolicySignal("foo", nil, nil, nil, spec.ActionGiveUp, 0, 3, 0)
	delta, ok := got.Payload.Map()["attributes_delta"].(map[string]any)
	if !ok {
		t.Fatalf("attributes_delta missing or not map; payload=%+v", got.Payload)
	}
	if len(delta) != 0 {
		t.Fatalf("attributes_delta should be empty when nil passed; got %+v", delta)
	}
}

func TestErrorPolicySignal_PassShape(t *testing.T) {
	got := errorPolicySignal("foo", nil, nil, nil, spec.ActionPass, 0, 3, 0)
	if got.Type != signalpkg.TypePath("terminal/error/foo") {
		t.Fatalf("pass type: got %q", got.Type)
	}
}
