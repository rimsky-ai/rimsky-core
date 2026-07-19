// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type stubModeExecutor struct {
	genv1.UnimplementedExecutorServer
	nextAckID int64
}

func (s *stubModeExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["probe_async"] == true {
		id := atomic.AddInt64(&s.nextAckID, 1)
		ackID := fmt.Sprintf("ack-%d", id)
		go deliverAsyncSuccess(req.GetCallbackUrl(), ackID)
		return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId: ackID,
		}}}, nil
	}
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "stubModeExecutor: stub probe",
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "stubModeExecutor: default",
	}}}, nil
}

func deliverAsyncSuccess(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"stub": true},
			"changed":          false,
			"change_summary":   "stubModeExecutor: async settle",
		},
	})
	resp, err := http.Post(callbackURL+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func startStubModeExecutor(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, &stubModeExecutor{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestUnaryProtocolScenarios_RunAgainstALiveExecutor(t *testing.T) {
	addr := startStubModeExecutor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint:        conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		RequireStubMode: true,
		Only:            []string{"tags_round_trip", "attributes_serialization", "async_callback_survives_restart"},
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}

	byName := make(map[string]conformance.Result, len(results))
	for _, r := range results {
		byName[r.Scenario] = r
	}

	for _, name := range []string{"tags_round_trip", "attributes_serialization", "async_callback_survives_restart"} {
		r, ok := byName[name]
		if !ok {
			t.Errorf("scenario %q did not run at all (not registered or filtered out); registered scenarios: %v", name, scenarioNames(results))
			continue
		}
		if r.Skipped {
			t.Errorf("scenario %q was skipped against a live stub-mode executor (%s)", name, r.Error)
			continue
		}
		if !r.Passed {
			t.Errorf("scenario %q failed against a live stub-mode executor: %s", name, r.Error)
		}
	}
}

func scenarioNames(results []conformance.Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Scenario)
	}
	return out
}
