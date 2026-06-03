// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub/stubtest"
)

// fakeProber is a Prober stub for testing the handshake without dialing
// real peers. Errors are guarded by sync.Mutex (atomic.Value chokes on
// typed-nil swaps).
type fakeProber struct {
	mu            sync.Mutex
	executorErr   error
	storeErr      error
	executorCaps  *ObservabilityCapabilities
	storeCaps     *ObservabilityCapabilities
	probeAttempts atomic.Int64
}

func newFakeProber() *fakeProber {
	f := &fakeProber{
		executorCaps: &ObservabilityCapabilities{SupportsTraceGet: true, RetentionAfterTerminalSeconds: 60},
		storeCaps:    &ObservabilityCapabilities{SupportsClaimGet: true, RetentionAfterTerminalSeconds: 60},
	}
	return f
}

func (f *fakeProber) setExecutorErr(err error) {
	f.mu.Lock()
	f.executorErr = err
	f.mu.Unlock()
}

func (f *fakeProber) ProbeExecutor(_ context.Context, _ string) (*ObservabilityCapabilities, error) {
	f.probeAttempts.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.executorErr != nil {
		return nil, f.executorErr
	}
	return f.executorCaps, nil
}

func (f *fakeProber) ProbeStore(_ context.Context, _ string) (*ObservabilityCapabilities, error) {
	f.probeAttempts.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	return f.storeCaps, nil
}

func TestRunHandshake_ReachableExecutor(t *testing.T) {
	prober := newFakeProber()
	disc := RunHandshake(context.Background(), prober,
		[]PeerSpec{{Name: "claude-agent", Endpoint: "claude-agent:9090"}},
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

func TestRunHandshake_UnreachableExecutor_NoError(t *testing.T) {
	prober := newFakeProber()
	prober.setExecutorErr(errors.New("dial timeout"))
	disc := RunHandshake(context.Background(), prober,
		[]PeerSpec{{Name: "x", Endpoint: "host:9090"}},
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
		[]PeerSpec{{Name: "x", Endpoint: "host:9090"}},
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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ = disc.GetExecutor("x")
		if got.Reachability == ReachabilityReachable {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("RefreshLoop did not heal; final reachability = %s", got.Reachability)
}

// TestHandshake_RealProberCachesAndHeals drives the real NewGRPCProber
// against a real loopback peer (no fakeProber), proving the production
// probe→cache path actually dials, reads the peer's advertised
// observability capabilities over the wire, and caches them — and that
// RefreshLoop flips the entry to unreachable once the peer dies. This is
// the coupling the gate closes: every other handshake test injects a
// fakeProber, so without this one nothing exercises gRPCProber end to end.
func TestHandshake_RealProberCachesAndHeals(t *testing.T) {
	srv, addr := stubtest.Listen(t, stub.New())

	disc := RunHandshake(context.Background(), NewGRPCProber(),
		[]PeerSpec{{Name: "x", Endpoint: addr}},
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
	// Assert on the wire-advertised caps the real stub serves
	// (observability.go Capabilities), NOT SupportsTraceGet — the stub
	// advertises that false; only the fakeProber set it true. Matching
	// DeclaredEvents/ExpectedAttributesSchema proves the real caps round-
	// tripped the gRPC boundary into the cache.
	wantEvents := []string{"ready", "signal", "checkpoint", "progress", "completed"}
	if !reflect.DeepEqual(entry.Capabilities.DeclaredEvents, wantEvents) {
		t.Fatalf("DeclaredEvents = %v, want %v", entry.Capabilities.DeclaredEvents, wantEvents)
	}
	if len(entry.Capabilities.ExpectedAttributesSchema) == 0 {
		t.Fatalf("ExpectedAttributesSchema empty — caps not probed over the wire")
	}

	// Heal/flip: kill the peer, run one RefreshLoop interval against the
	// real prober, and assert the entry flips to unreachable. Mirrors
	// TestRefreshLoop_HealsUnreachable but exercises the real dial path.
	srv.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disc.RefreshLoop(ctx, 50*time.Millisecond, slog.Default())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entry, _ = disc.GetExecutor("x")
		if entry.Reachability == ReachabilityUnreachable {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("RefreshLoop did not flip to unreachable; final reachability = %s", entry.Reachability)
}
