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
	storeClasses  []string
	storeClassErr error
	probeAttempts atomic.Int64
	// Received tlsMode arguments, per probe verb. The handshake must
	// thread each PeerSpec's TLS mode into every dial — dropping it at
	// any of the three probe call sites would silently downgrade a
	// required-TLS peer to plaintext.
	executorTLSModes   []string
	storeTLSModes      []string
	storeClassTLSModes []string
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

func (f *fakeProber) ProbeStore(_ context.Context, _, _, tlsMode string) (*ObservabilityCapabilities, error) {
	f.probeAttempts.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeTLSModes = append(f.storeTLSModes, tlsMode)
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	return f.storeCaps, nil
}

func (f *fakeProber) ProbeStoreDeclaredErrorClasses(_ context.Context, _, _, tlsMode string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeClassTLSModes = append(f.storeClassTLSModes, tlsMode)
	if f.storeClassErr != nil {
		return nil, f.storeClassErr
	}
	return f.storeClasses, nil
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

// TestRunHandshake_StoreDeclaredErrorClasses pins the producer half of
// TD-producer-declared-classes-capability: the ClaimProducer
// Capabilities handshake's declared_error_classes land in the
// discovery cache alongside the observability capabilities.
func TestRunHandshake_StoreDeclaredErrorClasses(t *testing.T) {
	prober := newFakeProber()
	prober.storeClasses = []string{"pg/claim_unavailable", "pg/swap_failed"}
	disc := RunHandshake(context.Background(), prober,
		nil,
		[]PeerSpec{{Name: "items-store", Endpoint: "items-store:9090"}},
		slog.Default(),
	)
	got, ok := disc.GetStore("items-store")
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

// TestRunHandshake_StoreDeclaredErrorClasses_ObsUnreachable pins that
// the producer vocabulary survives a missing observability surface:
// the validator must see the same vocabulary the runtime routes by
// even when the store exposes no observability endpoint.
func TestRunHandshake_StoreDeclaredErrorClasses_ObsUnreachable(t *testing.T) {
	prober := newFakeProber()
	prober.storeErr = errors.New("obs endpoint unreachable")
	prober.storeClasses = []string{"pg/claim_unavailable"}
	disc := RunHandshake(context.Background(), prober,
		nil,
		[]PeerSpec{{Name: "items-store", Endpoint: "items-store:9090"}},
		slog.Default(),
	)
	got, _ := disc.GetStore("items-store")
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

// TestRunHandshake_ThreadsTLSMode pins the TLS-mode threading: every
// probe call site (executor probe, store observability probe, store
// declared-error-classes probe) must receive the PeerSpec's TLS mode.
// Dropping the mode at any site would pass this suite before this test
// existed — the fake recorded nothing.
func TestRunHandshake_ThreadsTLSMode(t *testing.T) {
	prober := newFakeProber()
	_ = RunHandshake(context.Background(), prober,
		[]PeerSpec{{Name: "exec-tls", Endpoint: "exec:9090", TLS: "required"}},
		[]PeerSpec{{Name: "store-tls", Endpoint: "store:9090", TLS: "required"}},
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
	if len(prober.storeTLSModes) == 0 {
		t.Fatalf("store probe never called")
	}
	for _, m := range prober.storeTLSModes {
		if m != "required" {
			t.Fatalf("store probe received tlsMode %q, want required", m)
		}
	}
	if len(prober.storeClassTLSModes) == 0 {
		t.Fatalf("store declared-error-classes probe never called")
	}
	for _, m := range prober.storeClassTLSModes {
		if m != "required" {
			t.Fatalf("store declared-error-classes probe received tlsMode %q, want required", m)
		}
	}
}
