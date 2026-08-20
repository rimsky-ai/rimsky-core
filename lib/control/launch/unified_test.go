// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package launch

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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
		failCh: make(chan RoleFailure, 3),
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
		failCh: make(chan RoleFailure, 3),
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
	runSchedulerFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig, _ persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return record(d)
	}
	runSupervisorFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig, _ *config.BundledRegistrations, _ persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return record(d)
	}
	runControlAPIFn = func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig, _ *config.BundledRegistrations, _ persistence.BlobBackend) (StopFunc, <-chan error, error) {
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

func TestStartUnifiedStack_BlobBackendOpenedOnceAndSharedAcrossRunners(t *testing.T) {
	origScheduler, origSupervisor, origControlAPI := runSchedulerFn, runSupervisorFn, runControlAPIFn
	defer func() {
		runSchedulerFn = origScheduler
		runSupervisorFn = origSupervisor
		runControlAPIFn = origControlAPI
	}()

	var (
		mu        sync.Mutex
		seenBlobs []persistence.BlobBackend
		fakeStop  = func(context.Context) error { return nil }
	)
	record := func(bb persistence.BlobBackend) (StopFunc, <-chan error, error) {
		mu.Lock()
		seenBlobs = append(seenBlobs, bb)
		mu.Unlock()
		return fakeStop, make(chan error), nil
	}
	runSchedulerFn = func(_ context.Context, _ *slog.Logger, _ persistence.Database, _ *config.RimskyConfig, bb persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return record(bb)
	}
	runSupervisorFn = func(_ context.Context, _ *slog.Logger, _ persistence.Database, _ *config.RimskyConfig, _ *config.BundledRegistrations, bb persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return record(bb)
	}
	runControlAPIFn = func(_ context.Context, _ *slog.Logger, _ persistence.Database, _ *config.RimskyConfig, _ *config.BundledRegistrations, bb persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return record(bb)
	}

	setCalls := 0
	driver := &fakeDriver{onSetBlobBackend: func() { setCalls++ }}
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(context.Background(), logger, driver, &config.RimskyConfig{}, &config.BundledRegistrations{})
	if err != nil {
		t.Fatalf("StartUnifiedStack: %v", err)
	}
	defer stack.Drain(context.Background(), time.Second)

	if setCalls != 1 {
		t.Fatalf("driver.SetBlobBackend called %d times, want exactly 1 (each additional call is an unsynchronized field write racing already-running roles' readers)", setCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenBlobs) != 3 {
		t.Fatalf("want 3 runner invocations, got %d", len(seenBlobs))
	}
	for i, bb := range seenBlobs {
		if bb == nil {
			t.Fatalf("runner[%d] received a nil blob backend", i)
		}
		if bb != seenBlobs[0] {
			t.Errorf("runner[%d] blob backend = %v, want the same pre-opened instance as runner[0] = %v", i, bb, seenBlobs[0])
		}
	}
}

type fakeDriver struct {
	persistence.Database
	onSetBlobBackend func()
}

func (f *fakeDriver) SetBlobBackend(persistence.BlobBackend, int, time.Duration) {
	if f.onSetBlobBackend != nil {
		f.onSetBlobBackend()
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestUnifiedStack_FailChDeliversFailure(t *testing.T) {
	stack := &UnifiedStack{
		failCh: make(chan RoleFailure, 3),
	}
	want := RoleFailure{Role: "supervisor", Err: errors.New("serve loop died")}
	stack.failCh <- want
	if got := <-stack.FailCh(); got.Role != want.Role || got.Err.Error() != want.Err.Error() {
		t.Fatalf("FailCh delivered %+v, want %+v", got, want)
	}
}

func stubRunners() (schedulerFn func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, persistence.BlobBackend) (StopFunc, <-chan error, error),
	supervisorFn func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error),
	controlAPIFn func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error),
) {
	fakeStop := func(context.Context) error { return nil }
	return func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, persistence.BlobBackend) (StopFunc, <-chan error, error) {
			return fakeStop, make(chan error), nil
		},
		func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
			return fakeStop, make(chan error), nil
		},
		func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
			return fakeStop, make(chan error), nil
		}
}

func TestStartUnifiedStack_ForwardsRunnerFailure(t *testing.T) {
	origScheduler, origSupervisor, origControlAPI := runSchedulerFn, runSupervisorFn, runControlAPIFn
	defer func() {
		runSchedulerFn = origScheduler
		runSupervisorFn = origSupervisor
		runControlAPIFn = origControlAPI
	}()

	schedFailCh := make(chan error)
	fakeStop := func(context.Context) error { return nil }
	runSchedulerFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return fakeStop, schedFailCh, nil
	}
	_, supervisorFn, controlAPIFn := stubRunners()
	runSupervisorFn = supervisorFn
	runControlAPIFn = controlAPIFn

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(context.Background(), logger, &fakeDriver{}, &config.RimskyConfig{}, &config.BundledRegistrations{})
	if err != nil {
		t.Fatalf("StartUnifiedStack: %v", err)
	}
	defer stack.Drain(context.Background(), time.Second)

	want := errors.New("scheduler serve loop died")
	schedFailCh <- want

	got := <-stack.FailCh()
	if got.Role != "scheduler" || got.Err.Error() != want.Error() {
		t.Fatalf("forwarded failure = %+v, want role=scheduler err=%v", got, want)
	}
}

func TestStartUnifiedStack_IgnoresNilAndClosedRunnerFailure(t *testing.T) {
	origScheduler, origSupervisor, origControlAPI := runSchedulerFn, runSupervisorFn, runControlAPIFn
	defer func() {
		runSchedulerFn = origScheduler
		runSupervisorFn = origSupervisor
		runControlAPIFn = origControlAPI
	}()

	schedFailCh := make(chan error)
	supFailCh := make(chan error)
	fakeStop := func(context.Context) error { return nil }
	runSchedulerFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return fakeStop, schedFailCh, nil
	}
	runSupervisorFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return fakeStop, supFailCh, nil
	}
	_, _, controlAPIFn := stubRunners()
	runControlAPIFn = controlAPIFn

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(context.Background(), logger, &fakeDriver{}, &config.RimskyConfig{}, &config.BundledRegistrations{})
	if err != nil {
		t.Fatalf("StartUnifiedStack: %v", err)
	}
	defer stack.Drain(context.Background(), time.Second)

	schedFailCh <- nil
	close(supFailCh)

	select {
	case got := <-stack.FailCh():
		t.Fatalf("nil/closed runner errors should not be forwarded, got %+v", got)
	default:
	}
}

func TestStartUnifiedStack_CtxCancelDrainsStartedRoles(t *testing.T) {
	origScheduler, origSupervisor, origControlAPI := runSchedulerFn, runSupervisorFn, runControlAPIFn
	defer func() {
		runSchedulerFn = origScheduler
		runSupervisorFn = origSupervisor
		runControlAPIFn = origControlAPI
	}()

	stoppedCh := make(chan string, 3)
	makeStop := func(name string) StopFunc {
		return func(context.Context) error {
			stoppedCh <- name
			return nil
		}
	}
	runSchedulerFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return makeStop("scheduler"), make(chan error), nil
	}
	runSupervisorFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return makeStop("supervisor"), make(chan error), nil
	}
	runControlAPIFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return makeStop("control-api"), make(chan error), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(ctx, logger, &fakeDriver{}, &config.RimskyConfig{}, &config.BundledRegistrations{})
	if err != nil {
		t.Fatalf("StartUnifiedStack: %v", err)
	}
	_ = stack

	cancel()

	seen := map[string]bool{}
	for len(seen) < 3 {
		seen[<-stoppedCh] = true
	}
	for _, want := range []string{"scheduler", "supervisor", "control-api"} {
		if !seen[want] {
			t.Errorf("cancelling the startup ctx did not drain role %q", want)
		}
	}
}

func TestStartUnifiedStack_StartupFailureDrainsStartedRoles(t *testing.T) {
	origScheduler, origSupervisor, origControlAPI := runSchedulerFn, runSupervisorFn, runControlAPIFn
	defer func() {
		runSchedulerFn = origScheduler
		runSupervisorFn = origSupervisor
		runControlAPIFn = origControlAPI
	}()

	var (
		mu               sync.Mutex
		stopped          []string
		controlAPICalled bool
	)
	runSchedulerFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		stop := func(context.Context) error {
			mu.Lock()
			stopped = append(stopped, "scheduler")
			mu.Unlock()
			return nil
		}
		return stop, make(chan error), nil
	}
	supervisorErr := errors.New("supervisor boom")
	runSupervisorFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		return nil, nil, supervisorErr
	}
	runControlAPIFn = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations, persistence.BlobBackend) (StopFunc, <-chan error, error) {
		mu.Lock()
		controlAPICalled = true
		mu.Unlock()
		return nil, nil, nil
	}

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := StartUnifiedStack(context.Background(), logger, &fakeDriver{}, &config.RimskyConfig{}, &config.BundledRegistrations{})
	if stack != nil {
		t.Fatalf("StartUnifiedStack returned non-nil stack on startup failure: %+v", stack)
	}
	if err == nil || !strings.Contains(err.Error(), "start supervisor") {
		t.Fatalf("err = %v, want wrapped 'start supervisor'", err)
	}
	if !errors.Is(err, supervisorErr) {
		t.Fatalf("err = %v, want wraps %v", err, supervisorErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(stopped) != 1 || stopped[0] != "scheduler" {
		t.Fatalf("stopped = %v, want [scheduler] (already-started roles drained in reverse)", stopped)
	}
	if controlAPICalled {
		t.Fatal("control-api runner should never be invoked once supervisor failed to start")
	}
}
