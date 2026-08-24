// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package launch

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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

func TestStartUnifiedStack_OneDriverAcrossRunners(t *testing.T) {
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
	recordRun := func(_ context.Context, _ *slog.Logger, d persistence.Database, _ *config.RimskyConfig, _ RoleOptions) (StopFunc, <-chan error, error) {
		return record(d)
	}

	sentinel := &fakeDriver{Database: openSQLiteDriverForTest(t)}
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := startUnifiedStack(context.Background(), logger, sentinel, &config.RimskyConfig{}, &config.BundledRegistrations{},
		roleRunners{scheduler: recordRun, supervisor: recordRun, controlAPI: recordRun})
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

type fakeDriver struct {
	persistence.Database
}

func openSQLiteDriverForTest(t *testing.T) persistence.Database {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "rimsky.yml")
	cfg := `persistence:
  driver: sqlite
  sqlite:
    path: ` + filepath.Join(dir, "state.db") + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RIMSKY_CONFIG", cfgPath)
	driver, _, err := OpenDriverFromEnv(context.Background(), testLogger(t))
	if err != nil {
		t.Fatalf("open sqlite driver: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	return driver
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

func stubRunners() roleRunners {
	fakeStop := func(context.Context) error { return nil }
	stub := func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
		return fakeStop, make(chan error), nil
	}
	return roleRunners{scheduler: stub, supervisor: stub, controlAPI: stub}
}

func TestStartUnifiedStack_ForwardsRunnerFailure(t *testing.T) {
	schedFailCh := make(chan error)
	fakeStop := func(context.Context) error { return nil }
	runs := stubRunners()
	runs.scheduler = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
		return fakeStop, schedFailCh, nil
	}

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := startUnifiedStack(context.Background(), logger, &fakeDriver{Database: openSQLiteDriverForTest(t)}, &config.RimskyConfig{}, &config.BundledRegistrations{}, runs)
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
	schedFailCh := make(chan error)
	supFailCh := make(chan error)
	fakeStop := func(context.Context) error { return nil }
	runs := stubRunners()
	runs.scheduler = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
		return fakeStop, schedFailCh, nil
	}
	runs.supervisor = func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
		return fakeStop, supFailCh, nil
	}

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := startUnifiedStack(context.Background(), logger, &fakeDriver{Database: openSQLiteDriverForTest(t)}, &config.RimskyConfig{}, &config.BundledRegistrations{}, runs)
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
	stoppedCh := make(chan string, 3)
	makeStop := func(name string) StopFunc {
		return func(context.Context) error {
			stoppedCh <- name
			return nil
		}
	}
	runs := roleRunners{
		scheduler: func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
			return makeStop("scheduler"), make(chan error), nil
		},
		supervisor: func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
			return makeStop("supervisor"), make(chan error), nil
		},
		controlAPI: func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
			return makeStop("control-api"), make(chan error), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := startUnifiedStack(ctx, logger, &fakeDriver{Database: openSQLiteDriverForTest(t)}, &config.RimskyConfig{}, &config.BundledRegistrations{}, runs)
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
	var (
		mu               sync.Mutex
		stopped          []string
		controlAPICalled bool
	)
	supervisorErr := errors.New("supervisor boom")
	runs := roleRunners{
		scheduler: func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
			stop := func(context.Context) error {
				mu.Lock()
				stopped = append(stopped, "scheduler")
				mu.Unlock()
				return nil
			}
			return stop, make(chan error), nil
		},
		supervisor: func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
			return nil, nil, supervisorErr
		},
		controlAPI: func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error) {
			mu.Lock()
			controlAPICalled = true
			mu.Unlock()
			return nil, nil, nil
		},
	}

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	stack, err := startUnifiedStack(context.Background(), logger, &fakeDriver{Database: openSQLiteDriverForTest(t)}, &config.RimskyConfig{}, &config.BundledRegistrations{}, runs)
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
