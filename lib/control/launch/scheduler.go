// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package launch holds the importable role runners: the full wiring of
// each rimsky role (scheduler, supervisor, control-api) lifted out of
// its cmd/ main so it can run either as a standalone role binary or
// in-process inside the single-process all-in-one entrypoint.
//
// Each RunX loads config from the standard env-var-named paths, starts
// the role via its lib/control/config Start* entry point plus the
// role-specific background loops (metrics gauge refresher, optional
// /metrics listener, the supervisor's observability handshake), and
// returns a stop handle that tears all of it down. The role mains
// shrink to logger setup + RunX + signal wait; the entrypoint's
// no-command path calls all three RunX in one process.
package launch

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"

	// @constraint: blank imports register the persistence drivers via
	// their init() functions.
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

// StopFunc tears down a running role: role handle shutdown, metrics
// listener shutdown, background-loop cancellation, driver close. The
// ctx bounds the graceful-shutdown wait (callers pass a deadline ctx).
type StopFunc func(ctx context.Context) error

// @agent-contract: each RunX returns a fail channel that surfaces a
// fatal post-start failure of the role (a serve loop dying, the
// metrics listener failing to bind). At most one error is ever sent; a
// clean shutdown sends nothing and the StopFunc closes the channel
// once teardown completes, so monitor goroutines reading it exit
// instead of leaking across embedded start/stop cycles. The role mains
// and the unified entrypoint select on it beside the signal channel so
// a dead role exits the process non-zero (container restart) instead
// of running on degraded.

// failureReporter serializes post-start failure reports onto a RunX
// fail channel. Producers (the metrics serve loop, role serve-loop
// monitors) call Report; the role's StopFunc calls Close once teardown
// is done so monitor goroutines reading the channel observe the close
// and exit — RunX is embeddable, and each start/stop cycle must not
// leak monitors. The mutex makes the Report/Close pair race-free: a
// Report racing Close becomes a silent no-op (the role is already
// stopping) rather than a send on a closed channel.
type failureReporter struct {
	mu     sync.Mutex
	closed bool
	ch     chan error
}

func newFailureReporter(capacity int) *failureReporter {
	return &failureReporter{ch: make(chan error, capacity)}
}

// Report delivers a fatal post-start failure onto the fail channel.
// Non-blocking: if the buffer is full (a failure is already pending)
// or the reporter is closed, the error is dropped — one failure is
// enough to restart the role.
func (f *failureReporter) Report(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	select {
	case f.ch <- err:
	default:
	}
}

// Close closes the fail channel so monitors exit. Idempotent.
func (f *failureReporter) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	close(f.ch)
}

// RunScheduler wires and starts the scheduler role: blob-backend
// install, config.StartScheduler, the metrics gauge refresher, and
// the optional /metrics listener. The persistence driver and parsed
// rimsky.yml config are supplied by the caller — see
// OpenDriverFromEnv. Errors are logged on the supplied logger
// (matching the standalone binary's output) and returned. The
// returned StopFunc shuts the role down.
//
// The runner does NOT open, close, or otherwise own the driver;
// that is the caller's responsibility. In unified mode a single
// driver is shared across all three Run* runners so the persistence
// writer slot is not contended across roles.
//
// Environment variables (identical to the rimsky-scheduler binary):
//
//	RIMSKY_SCHEDULER_TICK_MS    optional; default 1500.
//	RIMSKY_HEARTBEAT_TIMEOUT_MS optional; default 15000.
//	RIMSKY_SCHEDULER_ID         optional; default scheduler-<hostname>.
//	RIMSKY_METRICS_PORT         optional; default 0 = disabled (see
//	                            metricsPortFor for the unified-mode and
//	                            per-role override rules).
//	RIMSKY_METRICS_HOST         optional; default 127.0.0.1.
func RunScheduler(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig) (StopFunc, <-chan error, error) {
	tickMs := atoiDefault(os.Getenv("RIMSKY_SCHEDULER_TICK_MS"), 1500)
	log := shared.NewSlogLogger(logger)

	// @deliberate: resolve the /metrics port up-front so a malformed env
	// value fails the role at startup instead of silently disabling
	// metrics.
	metricsPort, err := metricsPortFor("scheduler")
	if err != nil {
		log.Error("metrics port resolution", "error", err.Error())
		return nil, nil, err
	}

	// @constraint: install BlobBackend on the driver. The scheduler does
	// not itself spill writes (it reads via SweepParkedNodes which hits
	// parked payload columns, but those go through
	// queue.LoadResumeMetadataInTx at the supervisor side). Installing
	// here keeps ValidateBlobConfig gating consistent across roles
	// (memory backend rejection, filesystem.root presence) and exposes
	// the backend on the driver — the scheduler-side orphan-blob sweep
	// deletes through it.
	blobBackend, err := config.OpenBlobBackend(rimskyCfg.Blob, driver)
	if err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		return nil, nil, err
	}

	supervisorID := os.Getenv("RIMSKY_SCHEDULER_ID")
	if supervisorID == "" {
		// @constraint: hostname-derived default so multi-replica
		// deployments don't collide on a single shared id. The
		// scheduler-tick advisory lock still single-writes against the
		// scheduler tick, but a per-replica id keeps audit-log rows and
		// orphan-claim attribution honest.
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			supervisorID = "scheduler-" + hostname
		} else {
			supervisorID = "scheduler-default"
		}
	}

	// @constraint: per-role Prometheus registry constructed up-front so
	// the scheduler's per-tick invalidate emits and frame.RunTick
	// observations land on the shared registry via the MetricsHook
	// adapter. The /metrics HTTP listener is opened below only when
	// RIMSKY_METRICS_PORT > 0; the registry itself is built
	// unconditionally so the hook stays wired even when the HTTP
	// surface is disabled.
	mreg := observability.NewMetricsRegistry()

	h, err := config.StartScheduler(config.SchedulerConfig{
		Driver:                  driver,
		Clock:                   shared.SystemClock{},
		Logger:                  log,
		TickInterval:            time.Duration(tickMs) * time.Millisecond,
		Stores:                  rimskyCfg.Stores,
		NamedLocks:              rimskyCfg.NamedLocks,
		SupervisorID:            supervisorID,
		Blob:                    blobBackend,
		OrphanBlobSweepInterval: rimskyCfg.Blob.Retention.OrphanSweepInterval,
		Metrics:                 observability.MetricsHookOf(mreg),
		Retention:               rimskyCfg.Retention,
	})
	if err != nil {
		log.Error("StartScheduler", "error", err.Error())
		return nil, nil, err
	}

	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	reporter := newFailureReporter(1)
	metricsSrv := startMetricsServer(metricsHostFromEnv(), "scheduler", metricsPort, mreg, log, reporter)

	stop := func(stopCtx context.Context) error {
		var firstErr error
		// @constraint: stopCtx's deadline is shared across both
		// servers — the scheduler handle's shutdown and the metrics
		// server's drain below.
		if err := h.Shutdown(stopCtx); err != nil {
			log.Error("scheduler shutdown", "error", err.Error())
			firstErr = err
		}
		if metricsSrv != nil {
			if err := metricsSrv.Shutdown(stopCtx); err != nil {
				log.Warn("metrics server shutdown", "error", err.Error())
			}
		}
		cancelGauges()
		// @constraint: the driver is caller-owned — RunScheduler MUST
		// NOT close it. Close the fail channel so monitor goroutines
		// reading it exit; RunScheduler is embeddable and must not leak
		// a monitor per start/stop cycle.
		reporter.Close()
		return firstErr
	}
	return stop, reporter.ch, nil
}

// metricsHostFromEnv resolves RIMSKY_METRICS_HOST with the 127.0.0.1
// default shared by the scheduler and supervisor roles.
func metricsHostFromEnv() string {
	host := os.Getenv("RIMSKY_METRICS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	return host
}

// metricsRoleOffsets gives each role a deterministic port offset from
// the shared RIMSKY_METRICS_PORT base in unified (single-process) mode.
// Without it the three in-process roles would race for one port and two
// would lose silently — three unscrapeable registries out of four.
var metricsRoleOffsets = map[string]int{
	"scheduler":   0,
	"supervisor":  1,
	"control-api": 2,
}

// metricsPortFor resolves the /metrics port for one role:
//
//   - RIMSKY_METRICS_PORT_<ROLE> (e.g. RIMSKY_METRICS_PORT_CONTROL_API)
//     wins when set — an explicit per-role port in any deployment shape.
//   - Otherwise RIMSKY_METRICS_PORT is the base; <= 0 disables metrics.
//   - In unified mode (RIMSKY_PROCESS_ROLE=unified, the single-process
//     all-in-one) the three roles share one environment, so the shared
//     base is offset per role: scheduler = base, supervisor = base+1,
//     control-api = base+2. In per-process deployments the base is used
//     as-is.
//
// A non-numeric value in either env var is a startup-fatal error: the
// operator asked for metrics and a silent fall-through to "disabled"
// (port 0) is exactly the silent metrics loss this resolution exists
// to prevent. Each RunX resolves the port up-front and fails fast.
func metricsPortFor(role string) (int, error) {
	perRoleVar := "RIMSKY_METRICS_PORT_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_"))
	if s := os.Getenv(perRoleVar); s != "" {
		port, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("invalid %s=%q: not a number", perRoleVar, s)
		}
		return port, nil
	}
	baseStr := os.Getenv("RIMSKY_METRICS_PORT")
	if baseStr == "" {
		return 0, nil
	}
	base, err := strconv.Atoi(baseStr)
	if err != nil {
		return 0, fmt.Errorf("invalid RIMSKY_METRICS_PORT=%q: not a number", baseStr)
	}
	if base <= 0 {
		return 0, nil
	}
	if os.Getenv("RIMSKY_PROCESS_ROLE") == "unified" {
		return base + metricsRoleOffsets[role], nil
	}
	return base, nil
}

// startMetricsServer opens the optional Prometheus /metrics listener
// for the named role on the caller-resolved port (see metricsPortFor;
// <= 0 = disabled, returns nil). Shared by all three role runners.
//
// @constraint: Net.Listen runs up-front BEFORE the serve goroutine
// launches, so a bind failure surfaces synchronously and the caller
// observes it on the next line. Calling srv.ListenAndServe from the
// goroutine would race the caller's failureReporter wiring — a fast
// bind-fail could push onto the reporter before the caller's monitor
// goroutine consumed it; for the scheduler's reporter (capacity 1) a
// concurrent serve-loop fail and a real role-fail would compete for
// the slot. Pre-binding pulls the bind error out of that race.
func startMetricsServer(host, role string, metricsPort int, mreg *observability.MetricsRegistry, log shared.Logger, report *failureReporter) *http.Server {
	if metricsPort <= 0 {
		return nil
	}
	metricsRouter := chi.NewRouter()
	observability.MountMetrics(metricsRouter, mreg)
	addr := fmt.Sprintf("%s:%d", host, metricsPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("metrics endpoint bind", "error", err.Error(), "role", role, "addr", addr)
		if report != nil {
			report.Report(fmt.Errorf("%s metrics endpoint bind %s: %w", role, addr, err))
		}
		return nil
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           metricsRouter,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("metrics endpoint listening", "addr", srv.Addr, "role", role)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("metrics endpoint", "error", err.Error(), "role", role)
			if report != nil {
				report.Report(fmt.Errorf("%s metrics endpoint: %w", role, err))
			}
		}
	}()
	return srv
}

// atoiDefault parses s as a positive integer, falling back to d.
func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return d
}
