// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifierhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: service-auth
func TestVerifierEgressGuardIsClosedByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	for _, k := range []string{
		"RIMSKY_EXECUTOR_HOST",
		"RIMSKY_EXECUTOR_PORT_GRPC",
		"RIMSKY_DAEMON_PORT",
		"RIMSKY_EXECUTOR_STUB_MODE",
		"RIMSKY_EXECUTOR_VERIFIER_HTTP_EGRESS_ALLOWLIST",
	} {
		t.Setenv(k, "")
	}
	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}

	attrs, err := structpb.NewStruct(map[string]any{"url": upstream.URL})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	outcome, err := NewServer(opts).Execute(context.Background(), &genv1.ExecuteRequest{Attributes: attrs})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	errOutcome := outcome.GetError()
	if errOutcome == nil {
		t.Fatalf("a loopback destination must be refused with no allowlist entry: the verifier dials its "+
			"node-attribute-supplied URL through the shared egress guard, default-closed like its sibling "+
			"dialers; got outcome %T", outcome.GetOutcome())
	}
	if !strings.Contains(strings.ToLower(errOutcome.GetPayload().AsMap()["message"].(string)), "blocked") &&
		!strings.Contains(strings.ToLower(errOutcome.GetErrorClass()), "transport") {
		t.Fatalf("refusal must name the egress block: class=%q payload=%v",
			errOutcome.GetErrorClass(), errOutcome.GetPayload().AsMap())
	}
}

// @concept: service-auth
func TestVerifierEgressAllowlistOptsALoopbackDestinationBackIn(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	for _, k := range []string{
		"RIMSKY_EXECUTOR_HOST",
		"RIMSKY_EXECUTOR_PORT_GRPC",
		"RIMSKY_DAEMON_PORT",
		"RIMSKY_EXECUTOR_STUB_MODE",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("RIMSKY_EXECUTOR_VERIFIER_HTTP_EGRESS_ALLOWLIST", "127.0.0.0/8,::1/128")
	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}

	attrs, err := structpb.NewStruct(map[string]any{"url": upstream.URL})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	outcome, err := NewServer(opts).Execute(context.Background(), &genv1.ExecuteRequest{Attributes: attrs})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("an operator that allowlists the loopback CIDR reaches the destination; got %v", outcome.GetError())
	}
}
