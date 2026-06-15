// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// RunControlAPI wires and starts the control-api role: blob-backend
// install, config.StartControlAPI, the metrics gauge refresher, and
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
// Environment variables (identical to the rimsky-control-api binary):
//
//	RIMSKY_CONTROL_API_HOST  optional; default 127.0.0.1.
//	RIMSKY_CONTROL_API_PORT  optional; default 8080. An explicit "0" also
//	                         selects the default; a non-numeric value
//	                         fails startup.
//	RIMSKY_METRICS_PORT      optional; default 0 = disabled (same host
//	                         as the control API; see metricsPortFor:
//	                         per-role override via
//	                         RIMSKY_METRICS_PORT_CONTROL_API; offset
//	                         base+2 in unified mode).
func RunControlAPI(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig) (StopFunc, <-chan error, error) {
	host := os.Getenv("RIMSKY_CONTROL_API_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	log := shared.NewSlogLogger(logger)

	// @constraint: default 8080; an explicit "0" also selects the
	// default. A non-empty unparseable value is a startup-fatal error —
	// silently mapping garbage to the default would hide the operator's
	// misconfiguration.
	port := 8080
	if s := os.Getenv("RIMSKY_CONTROL_API_PORT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			err = fmt.Errorf("invalid RIMSKY_CONTROL_API_PORT=%q: not a number", s)
			log.Error("control api port resolution", "error", err.Error())
			return nil, nil, err
		}
		if n != 0 {
			port = n
		}
	}

	// @deliberate: resolve the /metrics port up-front so a malformed env
	// value fails the role at startup instead of silently disabling
	// metrics.
	metricsPort, err := metricsPortFor("control-api")
	if err != nil {
		log.Error("metrics port resolution", "error", err.Error())
		return nil, nil, err
	}

	// @constraint: install BlobBackend on the driver so attribute writes
	// from control-api (e.g. instance-create-time fixture seeding via
	// raw store calls) honor the spill threshold. Validation is
	// identical across the three roles via ValidateBlobConfig.
	if _, err := config.OpenBlobBackend(rimskyCfg.Blob, driver); err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		return nil, nil, err
	}

	// @constraint: per-role Prometheus registry constructed up-front so
	// the control-api's admin-invalidate path can be instrumented via
	// the MetricsHook adapter. The /metrics HTTP listener is opened
	// below only when RIMSKY_METRICS_PORT > 0; the registry itself is
	// built unconditionally so the hook stays wired even when the HTTP
	// surface is disabled.
	mreg := observability.NewMetricsRegistry()

	h, err := config.StartControlAPI(config.ControlAPIConfig{
		Driver:     driver,
		Clock:      shared.SystemClock{},
		Logger:     log,
		Host:       host,
		Port:       port,
		Stores:     rimskyCfg.Stores,
		NamedLocks: rimskyCfg.NamedLocks,
		Executors:  rimskyCfg.Executors,
		// @constraint: publishers is the parsed top-level `publishers:`
		// block. Without it the publisher registry is empty and every
		// publisher-subscription (sensors included) fails at
		// instance-create with `unknown_publisher` — the entire
		// sensor/publisher feature is dead in any multi-process
		// deployment. The all-in-one and the three-container split both
		// run the control-api through this runner, so the registry MUST
		// be wired from the parsed config.
		Publishers: rimskyCfg.Publishers,
		Metrics:    observability.MetricsHookOf(mreg),

		LateBindServiceProxies: rimskyCfg.LateBindServiceProxies,
		// @constraint: operator-set registration-time
		// reference-validation mode parsed from
		// cfg:templates.ref_validation_mode (env-overridable via
		// env:RIMSKY_REF_VALIDATION_MODE). Default strict `all`.
		RefValidationMode: rimskyCfg.RefValidationMode,
	})
	if err != nil {
		log.Error("StartControlAPI", "error", err.Error())
		return nil, nil, err
	}
	log.Info("control api listening", "addr", h.Addr())

	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	// @deliberate: optional Prometheus /metrics endpoint on a separate
	// port (see metricsPortFor for resolution; 0 = disabled); binds the
	// same host as the control API. Capacity 2: the metrics serve loop
	// and the control-api serve loop can each report one failure.
	reporter := newFailureReporter(2)
	metricsSrv := startMetricsServer(host, "control-api", metricsPort, mreg, log, reporter)

	// @constraint: surface a fatal serve-loop failure (anything other
	// than graceful shutdown) as a role failure so the supervising
	// process exits non-zero instead of running on without its operator
	// surface. The handle's ServeErr channel is never closed on clean
	// shutdown, so the monitor also selects on a stop-owned done
	// channel — otherwise each embedded start/stop cycle would leak this
	// goroutine.
	stopped := make(chan struct{})
	var stoppedOnce sync.Once
	go func() {
		select {
		case err, ok := <-h.ServeErr():
			if ok && err != nil {
				reporter.Report(fmt.Errorf("control-api serve: %w", err))
			}
		case <-stopped:
		}
	}()

	stop := func(stopCtx context.Context) error {
		var firstErr error
		// @constraint: stopCtx's deadline is shared across both servers
		// — the control-api handle's shutdown and the metrics server's
		// drain below.
		if err := h.Shutdown(stopCtx); err != nil {
			log.Error("control api shutdown", "error", err.Error())
			firstErr = err
		}
		if metricsSrv != nil {
			if err := metricsSrv.Shutdown(stopCtx); err != nil {
				log.Warn("metrics server shutdown", "error", err.Error())
			}
		}
		cancelGauges()
		// @constraint: the driver is caller-owned — RunControlAPI MUST
		// NOT close it. Release the serve-loop monitor, then close the
		// fail channel so monitor goroutines reading it exit;
		// RunControlAPI is embeddable and must not leak goroutines per
		// start/stop cycle.
		stoppedOnce.Do(func() { close(stopped) })
		reporter.Close()
		return firstErr
	}
	return stop, reporter.ch, nil
}
