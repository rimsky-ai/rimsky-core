// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

func setupRoleStackEnv(t *testing.T) (runDir, endpoint string, port int) {
	t.Helper()
	runDir = t.TempDir()
	if err := compose.WriteSyntheticRimskyYAML(runDir, &compose.Manifest{Project: "test-launcher"}, nil, nil, 0); err != nil {
		t.Fatalf("write rimsky.yml: %v", err)
	}
	port, err := hostdaemon.FreeLocalPort()
	if err != nil {
		t.Fatalf("FreeLocalPort: %v", err)
	}
	endpoint = fmt.Sprintf("http://127.0.0.1:%d", port)
	t.Setenv("RIMSKY_CONFIG", filepath.Join(runDir, "rimsky.yml"))
	t.Setenv("RIMSKY_PROCESS_ROLE", "unified")
	t.Setenv("RIMSKY_CONTROL_API_HOST", "127.0.0.1")
	t.Setenv("RIMSKY_CONTROL_API_PORT", strconv.Itoa(port))
	t.Setenv("RIMSKY_METRICS_PORT", "")
	return runDir, endpoint, port
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestStartRoleStack_BootsAndDrains(t *testing.T) {
	runDir, endpoint, _ := setupRoleStackEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stack, err := compose.StartRoleStack(ctx, silentLogger(), filepath.Join(runDir, "rimsky.yml"), endpoint)
	if err != nil {
		t.Fatalf("StartRoleStack: %v", err)
	}
	if got := stack.Endpoint(); got != endpoint {
		t.Errorf("Endpoint() = %q, want %q", got, endpoint)
	}
	if err := compose.WaitForControlAPIReady(ctx, shared.SystemClock{}, stack.Endpoint(), 0); err != nil {
		stack.Drain(context.Background(), 5*time.Second)
		t.Fatalf("WaitForControlAPIReady: %v", err)
	}
	drainDone := make(chan struct{})
	go func() {
		stack.Drain(context.Background(), 5*time.Second)
		close(drainDone)
	}()
	<-drainDone
	select {
	case rf := <-stack.FailCh():
		t.Fatalf("unexpected role failure after clean drain: role=%s err=%v", rf.Role, rf.Err)
	default:
	}
}

func TestMigrationsRunBeforeRunners(t *testing.T) {
	runDir, endpoint, _ := setupRoleStackEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stack, err := compose.StartRoleStack(ctx, silentLogger(), filepath.Join(runDir, "rimsky.yml"), endpoint)
	if err != nil {
		t.Fatalf("StartRoleStack: %v", err)
	}
	t.Cleanup(func() { stack.Drain(context.Background(), 5*time.Second) })

	dbPath := filepath.Join(runDir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dbPath, err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rimsky_migrations`).Scan(&n); err != nil {
		t.Fatalf("query rimsky_migrations: %v (state.db at %s)", err, dbPath)
	}
	if n == 0 {
		t.Fatal("rimsky_migrations has zero rows — migrate did not run before StartRoleStack returned")
	}
}

func TestWaitForControlAPIReady_Polls(t *testing.T) {
	var hits atomic.Int64
	flipAt := int64(3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		if hits.Add(1) <= flipAt {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := compose.WaitForControlAPIReady(context.Background(), shared.SystemClock{}, srv.URL, 0); err != nil {
		t.Fatalf("WaitForControlAPIReady: %v", err)
	}
	if got := hits.Load(); got < flipAt {
		t.Errorf("poll-hits = %d, want at least %d (poll must have advanced past the 503 stretch)", got, flipAt)
	}
}

func TestWaitForControlAPIReady_DeadlineExceeded(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	clock := shared.NewAutoAdvanceClock(time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC))
	err := compose.WaitForControlAPIReady(context.Background(), clock, srv.URL, 150*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForControlAPIReady must error when the endpoint never returns 200")
	}
	if !strings.Contains(err.Error(), "not ready within 150ms") {
		t.Fatalf("err = %v, want the readiness deadline named", err)
	}
	if got := hits.Load(); got < 2 {
		t.Fatalf("health endpoint polled %d time(s); the wait must keep polling until its deadline, not give up on the first 503", got)
	}
}
