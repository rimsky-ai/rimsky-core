// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package scenarios_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

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
			Tags:            stubModeTags(attrs),
		}}}, nil
	}
	if attrs["probe_park"] == true {
		scratch := req.GetScratch()
		if len(scratch) == 0 {
			scratch = []byte("stubModeExecutor-park-scratch")
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
			ResumeAt: timestamppb.New(time.Now().Add(time.Hour)),
			Scratch:  scratch,
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "stubModeExecutor: default",
	}}}, nil
}

func stubModeTags(attrs map[string]any) []string {
	raw, ok := attrs["stub_tags"].([]any)
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}

type stubModeObservability struct {
	genv1.UnimplementedExecutorObservabilityServer
	declaredTags []string
}

func (o stubModeObservability) Capabilities(context.Context, *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{DeclaredTags: o.declaredTags}, nil
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
	postCallbackUntilAccepted(callbackURL, ackID, body)
}

func postCallbackUntilAccepted(callbackURL, ackID string, body []byte) {
	backoff := 10 * time.Millisecond
	for {
		resp, err := http.Post(callbackURL+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
		}
		//nolint:testwallclock-outcome inter-attempt cadence; this loop returns only when the receiver accepts the callback
		time.Sleep(backoff)
		if backoff < time.Second {
			backoff *= 3
		}
	}
}

func startStubModeExecutor(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, &stubModeExecutor{})
	genv1.RegisterExecutorObservabilityServer(srv, stubModeObservability{declaredTags: []string{"conformance-declared-tag"}})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestUnaryProtocolScenarios_RunAgainstALiveExecutor(t *testing.T) {
	addr := startStubModeExecutor(t)

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only: []string{"tags_round_trip", "attributes_serialization", "async_callback_survives_restart",
			"scratch_park_round_trip"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}

	byName := make(map[string]conformance.Result, len(results))
	for _, r := range results {
		byName[r.Scenario] = r
	}

	for _, name := range []string{"tags_round_trip", "attributes_serialization", "async_callback_survives_restart",
		"scratch_park_round_trip"} {
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

type tagDroppingExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (tagDroppingExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "tagDroppingExecutor: stub probe, tags dropped",
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "tagDroppingExecutor: default",
	}}}, nil
}

func startTagDroppingExecutor(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, tagDroppingExecutor{})
	genv1.RegisterExecutorObservabilityServer(srv, stubModeObservability{declaredTags: []string{"conformance-declared-tag"}})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestTagsRoundTrip_FailsWhenExecutorDropsDeclaredTag(t *testing.T) {
	addr := startTagDroppingExecutor(t)

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"tags_round_trip"},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "tags_round_trip")
	if row.Skipped {
		t.Fatalf("tags_round_trip: unexpectedly skipped: %s", row.Error)
	}
	if row.Passed {
		t.Fatalf("tags_round_trip: expected FAIL against an executor that declares a tag but never emits it on Success.tags, got PASS")
	}
}

func TestTagsRoundTrip_SkipsWhenExecutorDeclaresNoTags(t *testing.T) {
	addr := startStubModeExecutorNoDeclaredTags(t)

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"tags_round_trip"},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "tags_round_trip")
	if row.Skipped {
		t.Fatalf("tags_round_trip: unexpectedly skipped via the harness (expected the scenario itself to no-op): %s", row.Error)
	}
	if !row.Passed {
		t.Fatalf("tags_round_trip: expected PASS (no declared tags means nothing to round-trip), got: %s", row.Error)
	}
}

func startStubModeExecutorNoDeclaredTags(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, &stubModeExecutor{})
	genv1.RegisterExecutorObservabilityServer(srv, stubModeObservability{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

type emptyScratchParkExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (emptyScratchParkExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["probe_park"] == true {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
			ResumeAt: timestamppb.New(time.Now().Add(time.Hour)),
		}}}, nil
	}
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "emptyScratchParkExecutor: stub probe",
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "emptyScratchParkExecutor: default",
	}}}, nil
}

func startExecutor(t *testing.T, srv genv1.ExecutorServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterExecutorServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)
	return lis.Addr().String()
}

func TestScratchParkRoundTrip_FailsWhenExecutorEmitsEmptyScratch(t *testing.T) {
	addr := startExecutor(t, emptyScratchParkExecutor{})

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"scratch_park_round_trip"},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "scratch_park_round_trip")
	if row.Skipped {
		t.Fatalf("scratch_park_round_trip: unexpectedly skipped: %s", row.Error)
	}
	if row.Passed {
		t.Fatalf("scratch_park_round_trip: expected FAIL against an executor that always emits empty Park scratch, got PASS")
	}
}

type asyncScratchParkExecutor struct {
	genv1.UnimplementedExecutorServer
	nextAckID int64
}

func (s *asyncScratchParkExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "asyncScratchParkExecutor: stub probe",
		}}}, nil
	}
	if attrs["probe_park"] != true {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			ChangeSummary: "asyncScratchParkExecutor: default",
		}}}, nil
	}
	id := atomic.AddInt64(&s.nextAckID, 1)
	ackID := fmt.Sprintf("ack-%d", id)
	scratch := req.GetScratch()
	if len(scratch) == 0 {
		scratch = []byte("asyncScratchParkExecutor-park-scratch")
	}
	go deliverAsyncPark(req.GetCallbackUrl(), ackID, scratch)
	return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
		AsyncAckId: ackID,
	}}}, nil
}

func deliverAsyncPark(callbackURL, ackID string, scratch []byte) {
	if callbackURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"park": map[string]any{
			"resume_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"scratch":   base64.StdEncoding.EncodeToString(scratch),
		},
	})
	resp, err := http.Post(callbackURL+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func TestScratchParkRoundTrip_PassesThroughAsyncCallbackDecode(t *testing.T) {
	addr := startExecutor(t, &asyncScratchParkExecutor{})

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"scratch_park_round_trip"},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "scratch_park_round_trip")
	if row.Skipped {
		t.Fatalf("scratch_park_round_trip: unexpectedly skipped: %s", row.Error)
	}
	if !row.Passed {
		t.Fatalf("scratch_park_round_trip: expected PASS when the executor's async callback carries a "+
			"base64-encoded scratch field (proving parseCallbackBody preserves it), got: %s", row.Error)
	}
}

func findScenarioResult(t *testing.T, results []conformance.Result, name string) conformance.Result {
	t.Helper()
	for _, r := range results {
		if r.Scenario == name {
			return r
		}
	}
	t.Fatalf("scenario %q did not run at all; registered scenarios: %v", name, scenarioNames(results))
	return conformance.Result{}
}

type singleAttemptAsyncExecutor struct {
	genv1.UnimplementedExecutorServer
	nextAckID int64
}

func (s *singleAttemptAsyncExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "singleAttemptAsyncExecutor: stub probe",
		}}}, nil
	}
	if attrs["probe_async"] != true {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			ChangeSummary: "singleAttemptAsyncExecutor: default",
		}}}, nil
	}
	id := atomic.AddInt64(&s.nextAckID, 1)
	ackID := fmt.Sprintf("ack-%d", id)
	go deliverAsyncSuccessOnce(req.GetCallbackUrl(), ackID)
	return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
		AsyncAckId: ackID,
	}}}, nil
}

func deliverAsyncSuccessOnce(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"stub": true},
			"changed":          false,
			"change_summary":   "singleAttemptAsyncExecutor: async settle, single attempt",
		},
	})
	resp, err := http.Post(callbackURL+"/v1/callback/"+ackID, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func TestAsyncCallbackSurvivesRestart_FailsWhenExecutorDoesNotRetry(t *testing.T) {
	addr := startExecutor(t, &singleAttemptAsyncExecutor{})

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"async_callback_survives_restart"},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "async_callback_survives_restart")
	if row.Skipped {
		t.Fatalf("async_callback_survives_restart: unexpectedly skipped: %s", row.Error)
	}
	if row.Passed {
		t.Fatalf("async_callback_survives_restart: expected FAIL against an executor whose single callback " +
			"delivery attempt is dropped by the simulated restart (no retry), got PASS")
	}
}

type resumeAtHonoringParkExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (resumeAtHonoringParkExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "resumeAtHonoringParkExecutor: stub probe",
		}}}, nil
	}
	if attrs["probe_park"] == true {
		var resumeAt *timestamppb.Timestamp
		if raw, _ := attrs["park_resume_at"].(string); raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				resumeAt = timestamppb.New(t)
			}
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
			ResumeAt: resumeAt,
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "resumeAtHonoringParkExecutor: default",
	}}}, nil
}

func TestParkEmission_PassesWhenExecutorEchoesRequestedResumeAt(t *testing.T) {
	addr := startExecutor(t, resumeAtHonoringParkExecutor{})

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"park_emission"},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "park_emission")
	if row.Skipped {
		t.Fatalf("park_emission: unexpectedly skipped: %s", row.Error)
	}
	if !row.Passed {
		t.Fatalf("park_emission: expected PASS against an executor that echoes the requested "+
			"park_resume_at attribute back as Park.resume_at, got: %s", row.Error)
	}
}

type fixedResumeAtParkExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (fixedResumeAtParkExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if attrs["stub_probe"] == true {
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			ChangeSummary:   "fixedResumeAtParkExecutor: stub probe",
		}}}, nil
	}
	if attrs["probe_park"] == true {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
			ResumeAt: timestamppb.New(time.Now().UTC().Add(24 * time.Hour)),
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "fixedResumeAtParkExecutor: default",
	}}}, nil
}

func TestParkEmission_FailsWhenExecutorIgnoresRequestedResumeAt(t *testing.T) {
	addr := startExecutor(t, fixedResumeAtParkExecutor{})

	ctx := context.Background()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: conformance.Endpoint{Transport: "grpc", URL: "grpc://" + addr},
		Only:     []string{"park_emission"},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
	}
	row := findScenarioResult(t, results, "park_emission")
	if row.Skipped {
		t.Fatalf("park_emission: unexpectedly skipped: %s", row.Error)
	}
	if row.Passed {
		t.Fatalf("park_emission: expected FAIL against an executor that ignores the requested " +
			"park_resume_at attribute and always parks 24h out, got PASS")
	}
}
