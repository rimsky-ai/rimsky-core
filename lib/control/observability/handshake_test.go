// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub/stubtest"
)

type fakeProber struct {
	mu                    sync.Mutex
	executorErr           error
	producerErr           error
	executorCaps          *ObservabilityCapabilities
	producerCaps          *ObservabilityCapabilities
	producerClasses       []string
	producerClassErr      error
	probeAttempts         atomic.Int64
	executorTLSModes      []string
	producerTLSModes      []string
	producerClassTLSModes []string
}

func newFakeProber() *fakeProber {
	f := &fakeProber{
		executorCaps: &ObservabilityCapabilities{SupportsTraceGet: true, RetentionAfterTerminalSeconds: 60},
		producerCaps: &ObservabilityCapabilities{SupportsClaimGet: true, RetentionAfterTerminalSeconds: 60},
	}
	return f
}

func (f *fakeProber) setExecutorErr(err error) {
	f.mu.Lock()
	f.executorErr = err
	f.mu.Unlock()
}

func (f *fakeProber) ProbeExecutor(_ context.Context, _, _, tlsMode string) (*ObservabilityCapabilities, error) {
	f.probeAttempts.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executorTLSModes = append(f.executorTLSModes, tlsMode)
	if f.executorErr != nil {
		return nil, f.executorErr
	}
	return f.executorCaps, nil
}

func (f *fakeProber) ProbeClaimProducer(_ context.Context, _, _, tlsMode string) (*ObservabilityCapabilities, error) {
	f.probeAttempts.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.producerTLSModes = append(f.producerTLSModes, tlsMode)
	if f.producerErr != nil {
		return nil, f.producerErr
	}
	return f.producerCaps, nil
}

func (f *fakeProber) ProbeClaimProducerDeclaredErrorClasses(_ context.Context, _, _, tlsMode string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.producerClassTLSModes = append(f.producerClassTLSModes, tlsMode)
	if f.producerClassErr != nil {
		return nil, f.producerClassErr
	}
	return f.producerClasses, nil
}

func TestRunHandshake_ReachableExecutor(t *testing.T) {
	prober := newFakeProber()
	disc := RunHandshake(context.Background(), prober,
		[]ServiceSpec{{Name: "claude-agent", Endpoint: "claude-agent:9090"}},
		nil,
		slog.Default(),
	)
	got, ok := disc.GetExecutor("claude-agent")
	if !ok {
		t.Fatalf("executor not in discovery")
	}
	if got.Reachability != ReachabilityReachable {
		t.Fatalf("reachability = %s, want reachable", got.Reachability)
	}
	if got.Capabilities == nil || !got.Capabilities.SupportsTraceGet {
		t.Fatalf("capabilities not populated: %+v", got.Capabilities)
	}
}

func TestRunHandshake_ClaimProducerDeclaredErrorClasses(t *testing.T) {
	prober := newFakeProber()
	prober.producerClasses = []string{"pg/claim_unavailable", "pg/swap_failed"}
	disc := RunHandshake(context.Background(), prober,
		nil,
		[]ServiceSpec{{Name: "items-store", Endpoint: "items-store:9090"}},
		slog.Default(),
	)
	got, ok := disc.GetClaimProducer("items-store")
	if !ok {
		t.Fatalf("store not in discovery")
	}
	if got.Capabilities == nil {
		t.Fatalf("capabilities not populated")
	}
	want := []string{"pg/claim_unavailable", "pg/swap_failed"}
	if len(got.Capabilities.DeclaredErrorClasses) != len(want) ||
		got.Capabilities.DeclaredErrorClasses[0] != want[0] ||
		got.Capabilities.DeclaredErrorClasses[1] != want[1] {
		t.Fatalf("declared_error_classes = %v, want %v", got.Capabilities.DeclaredErrorClasses, want)
	}
}

func TestRunHandshake_ClaimProducerDeclaredErrorClasses_ObsUnreachable(t *testing.T) {
	prober := newFakeProber()
	prober.producerErr = errors.New("obs endpoint unreachable")
	prober.producerClasses = []string{"pg/claim_unavailable"}
	disc := RunHandshake(context.Background(), prober,
		nil,
		[]ServiceSpec{{Name: "items-store", Endpoint: "items-store:9090"}},
		slog.Default(),
	)
	got, _ := disc.GetClaimProducer("items-store")
	if got.Reachability != ReachabilityUnreachable {
		t.Fatalf("reachability = %s, want unreachable", got.Reachability)
	}
	if got.Capabilities == nil || len(got.Capabilities.DeclaredErrorClasses) != 1 {
		t.Fatalf("declared_error_classes not cached despite unreachable obs probe: %+v", got.Capabilities)
	}
}

func TestRunHandshake_UnreachableExecutor_NoError(t *testing.T) {
	prober := newFakeProber()
	prober.setExecutorErr(errors.New("dial timeout"))
	disc := RunHandshake(context.Background(), prober,
		[]ServiceSpec{{Name: "x", Endpoint: "host:9090"}},
		nil,
		slog.Default(),
	)
	got, _ := disc.GetExecutor("x")
	if got.Reachability != ReachabilityUnreachable {
		t.Fatalf("reachability = %s, want unreachable", got.Reachability)
	}
	if got.Capabilities != nil {
		t.Fatalf("expected nil capabilities, got %+v", got.Capabilities)
	}
	if got.LastError == "" {
		t.Fatalf("expected LastError populated")
	}
}

func TestRefreshLoop_HealsUnreachable(t *testing.T) {
	prober := newFakeProber()
	prober.setExecutorErr(errors.New("initial fail"))
	disc := RunHandshake(context.Background(), prober,
		[]ServiceSpec{{Name: "x", Endpoint: "host:9090"}},
		nil,
		slog.Default(),
	)
	got, _ := disc.GetExecutor("x")
	if got.Reachability != ReachabilityUnreachable {
		t.Fatalf("expected unreachable initially")
	}
	prober.setExecutorErr(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disc.RefreshLoop(ctx, 50*time.Millisecond, slog.Default())
	awaited.Until(t, "the refresh loop to heal executor x back to reachable", func() bool {
		got, _ = disc.GetExecutor("x")
		return got.Reachability == ReachabilityReachable
	})
}

func TestRefreshLoop_SkipsStaticEntries(t *testing.T) {
	prober := newFakeProber()
	disc := NewDiscovery(prober)
	disc.SetExecutor(ServiceEntry{
		Name:         "static-exec",
		Endpoint:     "static-exec:9090",
		Reachability: ReachabilityUnreachable,
		Static:       true,
	})
	disc.SetClaimProducer(ServiceEntry{
		Name:         "static-store",
		Endpoint:     "static-store:9090",
		Reachability: ReachabilityUnreachable,
		Static:       true,
	})

	disc.refreshAll(context.Background(), slog.Default())

	if got := prober.probeAttempts.Load(); got != 0 {
		t.Fatalf("refreshAll probed %d time(s), want 0 (static entries must be skipped)", got)
	}
	gotExec, ok := disc.GetExecutor("static-exec")
	if !ok || gotExec.Reachability != ReachabilityUnreachable || !gotExec.LastProbedAt.IsZero() {
		t.Fatalf("static executor entry changed by refreshAll: %+v", gotExec)
	}
	gotStore, ok := disc.GetClaimProducer("static-store")
	if !ok || gotStore.Reachability != ReachabilityUnreachable || !gotStore.LastProbedAt.IsZero() {
		t.Fatalf("static store entry changed by refreshAll: %+v", gotStore)
	}
}

func TestHandshake_RealProberCachesAndHeals(t *testing.T) {
	srv, addr := stubtest.Listen(t, stub.New())

	disc := RunHandshake(context.Background(), NewGRPCProber(),
		[]ServiceSpec{{Name: "x", Endpoint: addr}},
		nil,
		slog.Default(),
	)

	entry, ok := disc.GetExecutor("x")
	if !ok {
		t.Fatalf("executor not in discovery")
	}
	if entry.Reachability != ReachabilityReachable {
		t.Fatalf("reachability = %s, want reachable (last error: %q)", entry.Reachability, entry.LastError)
	}
	if entry.Capabilities == nil {
		t.Fatalf("capabilities nil — nothing was probed over the wire")
	}
	wantTags := []string{"ready", "signal", "checkpoint", "progress", "completed"}
	if !reflect.DeepEqual(entry.Capabilities.DeclaredTags, wantTags) {
		t.Fatalf("DeclaredTags = %v, want %v", entry.Capabilities.DeclaredTags, wantTags)
	}
	if len(entry.Capabilities.ExpectedAttributesSchema) == 0 {
		t.Fatalf("ExpectedAttributesSchema empty — caps not probed over the wire")
	}

	srv.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disc.RefreshLoop(ctx, 50*time.Millisecond, slog.Default())
	awaited.Until(t, "the refresh loop to flip executor x to unreachable after its server stopped", func() bool {
		entry, _ = disc.GetExecutor("x")
		return entry.Reachability == ReachabilityUnreachable
	})
}

func TestRunHandshake_ThreadsTLSMode(t *testing.T) {
	prober := newFakeProber()
	_ = RunHandshake(context.Background(), prober,
		[]ServiceSpec{{Name: "exec-tls", Endpoint: "exec:9090", TLS: "required"}},
		[]ServiceSpec{{Name: "store-tls", Endpoint: "store:9090", TLS: "required"}},
		slog.Default(),
	)
	prober.mu.Lock()
	defer prober.mu.Unlock()
	if len(prober.executorTLSModes) == 0 {
		t.Fatalf("executor probe never called")
	}
	for _, m := range prober.executorTLSModes {
		if m != "required" {
			t.Fatalf("executor probe received tlsMode %q, want required", m)
		}
	}
	if len(prober.producerTLSModes) == 0 {
		t.Fatalf("claim-producer probe never called")
	}
	for _, m := range prober.producerTLSModes {
		if m != "required" {
			t.Fatalf("claim-producer probe received tlsMode %q, want required", m)
		}
	}
	if len(prober.producerClassTLSModes) == 0 {
		t.Fatalf("claim-producer declared-error-classes probe never called")
	}
	for _, m := range prober.producerClassTLSModes {
		if m != "required" {
			t.Fatalf("claim-producer declared-error-classes probe received tlsMode %q, want required", m)
		}
	}
}
