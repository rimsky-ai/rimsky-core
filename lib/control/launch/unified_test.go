// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestUnifiedStack_DrainReversesStartOrder(t *testing.T) {
	var (
		mu        sync.Mutex
		stopOrder []string
		makeStop  = func(name string) StopFunc {
			return func(ctx context.Context) error {
				mu.Lock()
				stopOrder = append(stopOrder, name)
				mu.Unlock()
				return nil
			}
		}
	)

	stack := &UnifiedStack{
		stops: []StopFunc{
			makeStop("scheduler"),
			makeStop("supervisor"),
			makeStop("control-api"),
		},
		names:    []string{"scheduler", "supervisor", "control-api"},
		failCh:   make(chan RoleFailure, 3),
		failBufN: 3,
	}

	stack.Drain(context.Background(), time.Second)

	wantOrder := []string{"control-api", "supervisor", "scheduler"}
	if len(stopOrder) != len(wantOrder) {
		t.Fatalf("stop count = %d, want %d (order = %v)", len(stopOrder), len(wantOrder), stopOrder)
	}
	for i, name := range wantOrder {
		if stopOrder[i] != name {
			t.Errorf("stop[%d] = %q, want %q (full order = %v)", i, stopOrder[i], name, stopOrder)
		}
	}
}

func TestUnifiedStack_DrainEmptyIsNoOp(t *testing.T) {
	stack := &UnifiedStack{
		failCh:   make(chan RoleFailure, 3),
		failBufN: 3,
	}
	stack.Drain(context.Background(), time.Second)
}

func TestStartUnifiedStack_OneDriverAcrossRunners(t *testing.T) {
	origScheduler, origSupervisor, origControlAPI := runSchedulerFn, runSupervisorFn, runControlAPIFn
	defer func() {
		runSchedulerFn = origScheduler
		runSupervisorFn = origSupervisor
		runControlAPIFn = origControlAPI
	}()

	var (
		mu          sync.Mutex
		seenDrivers []persistence.Database
		fakeStop    = func(context.Context) error { return nil }
	)
	record := func(d persistence.Database) (StopFunc, <-chan error, error) {
		mu.Lock()
		seenDrivers = append(seenDrivers, d)
		mu.Unlock()
		ch := make(chan error)
		return fakeStop, ch, nil
	}
	runSchedulerFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig) (StopFunc, <-chan error, error) {
		return record(d)
	}
	runSupervisorFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig, _ *config.BundledRegistrations) (StopFunc, <-chan error, error) {
		return record(d)
	}
	runControlAPIFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig, _ *config.BundledRegistrations) (StopFunc, <-chan error, error) {
		return record(d)
	}

	sentinel := &fakeDriver{}
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(context.Background(), logger, sentinel, &config.RimskyConfig{}, &config.BundledRegistrations{})
	if err != nil {
		t.Fatalf("StartUnifiedStack: %v", err)
	}
	defer stack.Drain(context.Background(), time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(seenDrivers) != 3 {
		t.Fatalf("want 3 runner invocations, got %d", len(seenDrivers))
	}
	for i, d := range seenDrivers {
		if d != persistence.Database(sentinel) {
			t.Errorf("runner[%d] received driver = %p, want sentinel %p (one-driver-per-process falsifier)",
				i, d, sentinel)
		}
	}
}

type fakeDriver struct{ persistence.Database }

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestUnifiedStack_FailChDeliversFailure(t *testing.T) {
	stack := &UnifiedStack{
		failCh:   make(chan RoleFailure, 3),
		failBufN: 3,
	}
	want := RoleFailure{Role: "supervisor", Err: errors.New("serve loop died")}
	stack.failCh <- want
	select {
	case got := <-stack.FailCh():
		if got.Role != want.Role || got.Err.Error() != want.Err.Error() {
			t.Fatalf("FailCh delivered %+v, want %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("FailCh did not deliver the queued failure")
	}
}
