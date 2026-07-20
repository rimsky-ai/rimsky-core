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

func RunControlAPI(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig, bundledRegs *config.BundledRegistrations, preOpenedBlob persistence.BlobBackend) (StopFunc, <-chan error, error) {
	host := os.Getenv("RIMSKY_CONTROL_API_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	log := shared.NewSlogLogger(logger)

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

	metricsPort, err := metricsPortFor("control-api", rimskyCfg.Topology)
	if err != nil {
		log.Error("metrics port resolution", "error", err.Error())
		return nil, nil, err
	}

	if preOpenedBlob == nil {
		if _, err := config.OpenBlobBackend(rimskyCfg.Blob, driver, rimskyCfg.Topology); err != nil {
			log.Error("config.OpenBlobBackend", "error", err.Error())
			return nil, nil, err
		}
	}

	mreg := observability.NewMetricsRegistry()

	h, err := config.StartControlAPI(config.ControlAPIConfig{
		Driver:         driver,
		Clock:          shared.SystemClock{},
		Logger:         log,
		Host:           host,
		Port:           port,
		ClaimProducers: rimskyCfg.ClaimProducers,
		NamedLocks:     rimskyCfg.NamedLocks,
		Executors:      rimskyCfg.Executors,
		Publishers:     rimskyCfg.Publishers,
		Validators:     rimskyCfg.Validators,
		DataProcessors: rimskyCfg.DataProcessors,
		Metrics:        observability.MetricsHookOf(mreg),

		LateBindServiceProxies:     rimskyCfg.LateBindServiceProxies,
		PeerAuth:                   rimskyCfg.PeerAuth,
		UnreachableValidatorPolicy: rimskyCfg.UnreachableValidatorPolicy,
		Bundled:                    bundledRegs,
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

	reporter := newFailureReporter(2)
	metricsSrv := startMetricsServer(host, "control-api", metricsPort, mreg, log, reporter)

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
		stoppedOnce.Do(func() { close(stopped) })
		reporter.Close()
		return firstErr
	}
	return stop, reporter.ch, nil
}
