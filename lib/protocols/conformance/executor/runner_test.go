// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSkipMatch(t *testing.T) {
	cases := []struct {
		name       string
		scenario   string
		only, skip []string
		want       bool
	}{
		{"no filters runs everything", "cancel", nil, nil, false},
		{"skip list wins", "cancel", nil, []string{"cancel"}, true},
		{"skip list leaves others alone", "other", nil, []string{"cancel"}, false},
		{"only list admits named scenario", "cancel", []string{"cancel"}, nil, false},
		{"only list excludes unnamed scenario", "other", []string{"cancel"}, nil, true},
		{"skip takes precedence over only", "cancel", []string{"cancel"}, []string{"cancel"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipMatch(tc.scenario, tc.only, tc.skip); got != tc.want {
				t.Fatalf("skipMatch(%q, only=%v, skip=%v) = %v, want %v",
					tc.scenario, tc.only, tc.skip, got, tc.want)
			}
		})
	}
}

func TestSummary_CountsAndFormatsEachStatus(t *testing.T) {
	results := []Result{
		{Scenario: "a", Passed: true, Duration: 10 * time.Millisecond},
		{Scenario: "b", Passed: false, Error: "boom"},
		{Scenario: "c", Skipped: true, Error: "not applicable"},
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	Summary(results, w)
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "[PASS] a") {
		t.Errorf("expected a PASS line for scenario a, got:\n%s", got)
	}
	if !strings.Contains(got, "[FAIL] b") || !strings.Contains(got, "boom") {
		t.Errorf("expected a FAIL line naming scenario b's error, got:\n%s", got)
	}
	if !strings.Contains(got, "[SKIP] c") || !strings.Contains(got, "not applicable") {
		t.Errorf("expected a SKIP line naming scenario c's reason, got:\n%s", got)
	}
	if !strings.Contains(got, "1 passed, 1 failed, 1 skipped") {
		t.Errorf("expected a summary tally line, got:\n%s", got)
	}
}

type probeFakeExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (probeFakeExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	switch req.GetNodeType() {
	case "conformance-probe":
		delta, _ := structpb.NewStruct(map[string]any{"stub": true})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			AttributesDelta: delta,
		}}}, nil
	case "conformance-probe-async":
		return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId: "probe-ack",
		}}}, nil
	default:
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{}}}, nil
	}
}

type nonStubFakeExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (nonStubFakeExecutor) Execute(context.Context, *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{}}}, nil
}

func startFakeExecutorGRPC(t *testing.T, srv genv1.ExecutorServer) string {
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

func newTestEnv(t *testing.T, client Client) Env {
	t.Helper()
	receiver, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	return Env{Client: client, Callbacks: receiver}
}

func TestProbeStubMode_DetectsStubTrue(t *testing.T) {
	addr := startFakeExecutorGRPC(t, probeFakeExecutor{})
	client, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()
	env := newTestEnv(t, client)

	stubOK, err := ProbeStubMode(context.Background(), env, 5*time.Second)
	if err != nil {
		t.Fatalf("ProbeStubMode: %v", err)
	}
	if !stubOK {
		t.Fatal("expected ProbeStubMode to detect stub:true in attributes_delta")
	}
}

func TestProbeStubMode_FalseWhenNotStub(t *testing.T) {
	addr := startFakeExecutorGRPC(t, nonStubFakeExecutor{})
	client, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()
	env := newTestEnv(t, client)

	stubOK, err := ProbeStubMode(context.Background(), env, 5*time.Second)
	if err != nil {
		t.Fatalf("ProbeStubMode: %v", err)
	}
	if stubOK {
		t.Fatal("expected ProbeStubMode to report false when the executor never signals stub:true")
	}
}

func TestProbeAsyncSupport_TrueOnAwaitAsync(t *testing.T) {
	addr := startFakeExecutorGRPC(t, probeFakeExecutor{})
	client, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()
	env := newTestEnv(t, client)

	if !probeAsyncSupport(context.Background(), env, 5*time.Second) {
		t.Fatal("expected probeAsyncSupport to report true when the executor returns AwaitAsync")
	}
}

func TestProbeAsyncSupport_FalseOnSyncOutcome(t *testing.T) {
	addr := startFakeExecutorGRPC(t, nonStubFakeExecutor{})
	client, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()
	env := newTestEnv(t, client)

	if probeAsyncSupport(context.Background(), env, 5*time.Second) {
		t.Fatal("expected probeAsyncSupport to report false when the executor never returns AwaitAsync")
	}
}

func TestRun_DialsProbesAndReturnsOneResultPerRegisteredScenario(t *testing.T) {
	addr := startFakeExecutorGRPC(t, probeFakeExecutor{})
	results, err := Run(context.Background(), RunnerOpts{
		Endpoint: Endpoint{Transport: "grpc", URL: addr},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, sc := range All() {
		found := false
		for _, r := range results {
			if r.Scenario == sc.Name {
				found = true
			}
		}
		if !found {
			t.Errorf("Run: no result for registered scenario %q", sc.Name)
		}
	}
	if len(results) != len(All()) {
		t.Fatalf("Run: expected exactly one result per registered scenario, got %d results for %d scenarios",
			len(results), len(All()))
	}
}

func TestRun_RefusesLiveEndpointByDefault(t *testing.T) {
	addr := startFakeExecutorGRPC(t, nonStubFakeExecutor{})
	_, err := Run(context.Background(), RunnerOpts{
		Endpoint: Endpoint{Transport: "grpc", URL: addr},
		Timeout:  5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected Run to refuse an executor that never signals stub:true — the gate is fail-closed by default")
	}
}

func TestRun_AllowLiveSkipsStubRequiringScenarios(t *testing.T) {
	Register(Scenario{
		Name:         "allow-live-stub-required-probe",
		RequiresStub: true,
		Run: func(context.Context, Env) error {
			t.Fatal("a stub-requiring scenario must not execute under AllowLive against a live executor")
			return nil
		},
	})
	t.Cleanup(func() { registered = registered[:len(registered)-1] })

	addr := startFakeExecutorGRPC(t, nonStubFakeExecutor{})
	results, err := Run(context.Background(), RunnerOpts{
		Endpoint:  Endpoint{Transport: "grpc", URL: addr},
		AllowLive: true,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run with AllowLive against a live executor: %v", err)
	}
	sawSkip := false
	for _, r := range results {
		if r.Scenario == "allow-live-stub-required-probe" {
			if !r.Skipped {
				t.Fatalf("stub-requiring scenario must skip under AllowLive against a live executor: %+v", r)
			}
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatal("expected the registered stub-requiring scenario to appear as skipped")
	}
}

func TestRun_DialErrorSurfaced(t *testing.T) {
	_, err := Run(context.Background(), RunnerOpts{
		Endpoint: Endpoint{Transport: "bogus", URL: "127.0.0.1:0"},
		Timeout:  time.Second,
	})
	if err == nil {
		t.Fatal("expected Run to surface a dial error for an unknown transport")
	}
}

func TestUnknownFilterNames_DetectsTypo(t *testing.T) {
	all := []Scenario{{Name: "execute_happy_path"}, {Name: "attributes_serialization"}}
	got := unknownFilterNames([]string{"execute_happy_pathh"}, nil, all)
	if len(got) != 1 || got[0] != "execute_happy_pathh" {
		t.Fatalf("unknownFilterNames = %v, want [execute_happy_pathh]", got)
	}
}

func TestUnknownFilterNames_AllKnownReturnsEmpty(t *testing.T) {
	all := []Scenario{{Name: "execute_happy_path"}, {Name: "attributes_serialization"}}
	got := unknownFilterNames([]string{"execute_happy_path"}, []string{"attributes_serialization"}, all)
	if len(got) != 0 {
		t.Fatalf("unknownFilterNames = %v, want empty", got)
	}
}

func TestUnknownFilterNames_ChecksBothOnlyAndSkip(t *testing.T) {
	all := []Scenario{{Name: "execute_happy_path"}}
	got := unknownFilterNames([]string{"typo_only"}, []string{"typo_skip"}, all)
	if len(got) != 2 {
		t.Fatalf("unknownFilterNames = %v, want 2 unknown names", got)
	}
}

func TestSummary_SkippedWithoutErrorOmitsEmptyParens(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "summary")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer func() { _ = f.Close() }()

	Summary([]Result{{Scenario: "filtered_out", Skipped: true}}, f)

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("expected at least one line of output")
	}
	line := scanner.Text()
	if want := "[SKIP] filtered_out"; line != want {
		t.Errorf("line=%q want=%q", line, want)
	}
}

func TestSummary_SkippedWithReasonKeepsParens(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "summary")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer func() { _ = f.Close() }()

	Summary([]Result{{Scenario: "needs_stub", Skipped: true, Error: "stub mode required"}}, f)

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("expected at least one line of output")
	}
	line := scanner.Text()
	if want := "[SKIP] needs_stub (stub mode required)"; line != want {
		t.Errorf("line=%q want=%q", line, want)
	}
}
