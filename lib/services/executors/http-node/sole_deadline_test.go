// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
)

// @decision: three-dispatch-deadlines
func TestOutboundCallBoundedOnlyByDispatchDeadline(t *testing.T) {
	if timeout := NewServer(Opts{Egress: egress.Guard{}}).client.Timeout; timeout != 0 {
		t.Fatalf("http-node client carries an independent client-level timeout (%v) beneath the per-node "+
			"sync_rpc_deadline — the per-node deadline is the sole bound on a synchronous dispatch's "+
			"outbound call; a hidden second ceiling silently defeats the declared one", timeout)
	}

	for _, k := range []string{
		"RIMSKY_EXECUTOR_HOST",
		"RIMSKY_EXECUTOR_PORT_GRPC",
		"RIMSKY_AGENT_PORT",
		"RIMSKY_EXECUTOR_PORT_HTTP",
		"RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES",
		"RIMSKY_EXECUTOR_STUB_MODE",
		"RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL",
		"RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD",
		"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST",
	} {
		t.Setenv(k, "")
	}
	envOpts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if timeout := NewServer(envOpts).client.Timeout; timeout != 0 {
		t.Fatalf("http-node client built from LoadOptsFromEnv carries an independent client-level timeout "+
			"(%v) beneath the per-node sync_rpc_deadline — the env-configured path must leave the sole "+
			"bound to the per-node deadline exactly as the Opts path does", timeout)
	}

	blocked := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(blocked)
	}))
	defer upstream.Close()

	guard, err := egress.NewGuard([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatalf("egress.NewGuard: %v", err)
	}
	s := NewServer(Opts{Egress: guard})

	attrs, err := structpb.NewStruct(map[string]any{"url": upstream.URL})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	outcome, err := s.Execute(ctx, &genv1.ExecuteRequest{
		NodeId: "n", InstanceId: "i", NodeType: "http",
		Attributes: attrs,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	errOutcome, ok := outcome.GetOutcome().(*genv1.Outcome_Error)
	if !ok {
		t.Fatalf("expected an Error outcome when the dispatch deadline expires mid-call, got %T", outcome.GetOutcome())
	}
	if !strings.Contains(errOutcome.Error.GetErrorClass(), "timeout") {
		t.Fatalf("expected a timeout error class from deadline expiry, got %q", errOutcome.Error.GetErrorClass())
	}
	<-blocked
}
