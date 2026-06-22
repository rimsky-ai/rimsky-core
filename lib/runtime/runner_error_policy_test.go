// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestErrorPolicySignal_RetryShape(t *testing.T) {
	got := errorPolicySignal("foo", map[string]any{"k": "v"}, nil, spec.ActionRetry, 1, 500)
	if got.Type != signalpkg.TypePath("transient/retry/1/foo") {
		t.Fatalf("retry type: got %q want transient/retry/1/foo", got.Type)
	}
	if got.Payload["attempt"].(int) != 1 {
		t.Fatalf("attempt: got %v want 1", got.Payload["attempt"])
	}
	if got.Payload["error_class"].(string) != "foo" {
		t.Fatalf("error_class: got %v want foo", got.Payload["error_class"])
	}
	if got.Payload["delay_ms"].(int) != 500 {
		t.Fatalf("delay_ms: got %v want 500", got.Payload["delay_ms"])
	}
}

func TestErrorPolicySignal_ReleaseAndRequeueShape(t *testing.T) {
	got := errorPolicySignal("acquire/unavailable", nil, nil, spec.ActionReleaseAndRequeue, 0, 0)
	if got.Type != signalpkg.TypePath("transient/release_and_requeue/acquire/unavailable") {
		t.Fatalf("release_and_requeue type: got %q", got.Type)
	}
	if got.Payload["error_class"].(string) != "acquire/unavailable" {
		t.Fatalf("error_class: got %v", got.Payload["error_class"])
	}
}

func TestErrorPolicySignal_GiveUpShape(t *testing.T) {
	got := errorPolicySignal("http/timeout", map[string]any{"status": 504}, nil, spec.ActionGiveUp, 0, 0)
	if got.Type != signalpkg.TypePath("terminal/error/http/timeout") {
		t.Fatalf("give_up type: got %q want terminal/error/http/timeout", got.Type)
	}
	if got.Payload["error_class"].(string) != "http/timeout" {
		t.Fatalf("error_class: got %v", got.Payload["error_class"])
	}
}

func TestErrorPolicySignal_PassShape(t *testing.T) {
	got := errorPolicySignal("foo", nil, nil, spec.ActionPass, 0, 0)
	if got.Type != signalpkg.TypePath("terminal/error/foo") {
		t.Fatalf("pass type: got %q", got.Type)
	}
}
