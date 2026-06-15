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

// TestUnifiedStack_DrainReversesStartOrder covers
// @blessed-invariant: unified-stack-reverse-drain — Drain MUST invoke
// the per-role StopFuncs in reverse of start order so the operator-
// facing control-api stops first, before the engines under it. The
// test records the order in which mocked stops fire and asserts the
// reversal. Without the reversal, an executor mid-request would race
// the supervisor's claim-loop teardown and surface a confusing 5xx to
// the operator at the moment the verb is exiting.
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

	// @constraint: start order matches the production
	// StartUnifiedStack — scheduler, supervisor, control-api.
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

// TestUnifiedStack_DrainEmptyIsNoOp guards the partial-startup path:
// if StartUnifiedStack fails before any role started, the returned
// UnifiedStack (constructed for the in-StartUnifiedStack rollback
// drain) has zero stops and Drain must be safe.
func TestUnifiedStack_DrainEmptyIsNoOp(t *testing.T) {
	stack := &UnifiedStack{
		failCh:   make(chan RoleFailure, 3),
		failBufN: 3,
	}
	// @constraint: should return without panic.
	stack.Drain(context.Background(), time.Second)
}

// TestStartUnifiedStack_OneDriverAcrossRunners is the direct
// structural exhibit for @blessed-invariant: one-driver-per-process:
// the three role-runner seams record the persistence.Database pointer
// they receive, and the test asserts all three observed the SAME
// pointer (driver identity). A refactor that accidentally opened a
// per-role driver — re-introducing sqlite writer-slot contention
// inside one process — would surface here as a pointer mismatch
// rather than as a flaky concurrent-writer scenario test.
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
		// @constraint: do not close ch — the stack's monitor goroutine
		// treats an ok=false receive as the clean-shutdown path. Leaving
		// ch open lets the goroutine block until the test returns, which
		// is fine.
		return fakeStop, ch, nil
	}
	runSchedulerFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig) (StopFunc, <-chan error, error) {
		return record(d)
	}
	runSupervisorFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig) (StopFunc, <-chan error, error) {
		return record(d)
	}
	runControlAPIFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig) (StopFunc, <-chan error, error) {
		return record(d)
	}

	sentinel := &fakeDriver{}
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(context.Background(), logger, sentinel, &config.RimskyConfig{})
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

// fakeDriver is a no-op persistence.Database used only for pointer-
// identity comparison in TestStartUnifiedStack_OneDriverAcrossRunners.
// Every method panics — the test never calls one.
type fakeDriver struct{ persistence.Database }

// discardWriter is an io.Writer sink that swallows everything; used to
// silence the slog.Logger threaded into StartUnifiedStack.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestUnifiedStack_FailChDeliversFailure covers the merged failure
// channel surface: a RoleFailure pushed onto the channel surfaces on
// FailCh in the expected shape.
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
