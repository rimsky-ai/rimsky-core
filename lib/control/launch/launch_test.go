// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestFailureReporter_Report(t *testing.T) {
	r := newFailureReporter(1)
	want := errors.New("serve loop died")
	r.Report(want)

	select {
	case got := <-r.ch:
		if got != want {
			t.Fatalf("received %v, want %v", got, want)
		}
	default:
		t.Fatal("Report did not deliver the error on the fail channel")
	}
}

func TestFailureReporter_Close(t *testing.T) {
	r := newFailureReporter(1)
	r.Close()

	if _, ok := <-r.ch; ok {
		t.Fatal("channel should be closed after Close")
	}

	r.Close()
}

func TestFailureReporter_ReportAfterClose(t *testing.T) {
	r := newFailureReporter(1)
	r.Close()

	r.Report(errors.New("late failure"))

	if _, ok := <-r.ch; ok {
		t.Fatal("Report after Close must not deliver on the closed channel")
	}
}

func TestFailureReporter_OverCapacity(t *testing.T) {
	r := newFailureReporter(1)
	first := errors.New("first failure")
	r.Report(first)
	r.Report(errors.New("second failure"))

	if got := <-r.ch; got != first {
		t.Fatalf("first received error = %v, want %v", got, first)
	}
	select {
	case extra := <-r.ch:
		t.Fatalf("over-capacity Report should drop the error, got %v", extra)
	default:
	}
}

func TestMetricsPortFor(t *testing.T) {
	clearEnv := func(t *testing.T) {
		t.Setenv("RIMSKY_METRICS_PORT", "")
		t.Setenv("RIMSKY_METRICS_PORT_SCHEDULER", "")
		t.Setenv("RIMSKY_METRICS_PORT_SUPERVISOR", "")
		t.Setenv("RIMSKY_METRICS_PORT_CONTROL_API", "")
		t.Setenv("RIMSKY_PROCESS_ROLE", "")
	}

	t.Run("default disabled when nothing is set", func(t *testing.T) {
		clearEnv(t)
		for _, role := range []string{"scheduler", "supervisor", "control-api"} {
			port, err := metricsPortFor(role)
			if err != nil {
				t.Fatalf("metricsPortFor(%q): %v", role, err)
			}
			if port != 0 {
				t.Errorf("metricsPortFor(%q) = %d, want 0 (disabled)", role, port)
			}
		}
	})

	t.Run("base port used as-is per-process", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RIMSKY_METRICS_PORT", "9100")
		for _, role := range []string{"scheduler", "supervisor", "control-api"} {
			port, err := metricsPortFor(role)
			if err != nil {
				t.Fatalf("metricsPortFor(%q): %v", role, err)
			}
			if port != 9100 {
				t.Errorf("metricsPortFor(%q) = %d, want 9100", role, port)
			}
		}
	})

	t.Run("base <= 0 disables", func(t *testing.T) {
		clearEnv(t)
		for _, base := range []string{"0", "-1"} {
			t.Setenv("RIMSKY_METRICS_PORT", base)
			port, err := metricsPortFor("scheduler")
			if err != nil {
				t.Fatalf("metricsPortFor with base %q: %v", base, err)
			}
			if port != 0 {
				t.Errorf("metricsPortFor with base %q = %d, want 0 (disabled)", base, port)
			}
		}
	})

	t.Run("per-role override wins over base", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RIMSKY_METRICS_PORT", "9100")
		t.Setenv("RIMSKY_METRICS_PORT_CONTROL_API", "9200")

		port, err := metricsPortFor("control-api")
		if err != nil {
			t.Fatalf("metricsPortFor(control-api): %v", err)
		}
		if port != 9200 {
			t.Errorf("metricsPortFor(control-api) = %d, want per-role override 9200", port)
		}
		port, err = metricsPortFor("scheduler")
		if err != nil {
			t.Fatalf("metricsPortFor(scheduler): %v", err)
		}
		if port != 9100 {
			t.Errorf("metricsPortFor(scheduler) = %d, want base 9100", port)
		}
	})

	t.Run("unified mode offsets the shared base per role", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RIMSKY_METRICS_PORT", "9100")
		t.Setenv("RIMSKY_PROCESS_ROLE", "unified")
		for role, want := range map[string]int{
			"scheduler":   9100,
			"supervisor":  9101,
			"control-api": 9102,
		} {
			port, err := metricsPortFor(role)
			if err != nil {
				t.Fatalf("metricsPortFor(%q): %v", role, err)
			}
			if port != want {
				t.Errorf("unified metricsPortFor(%q) = %d, want %d", role, port, want)
			}
		}
	})

	t.Run("per-role override ignores the unified offset", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RIMSKY_METRICS_PORT", "9100")
		t.Setenv("RIMSKY_PROCESS_ROLE", "unified")
		t.Setenv("RIMSKY_METRICS_PORT_SUPERVISOR", "9500")

		port, err := metricsPortFor("supervisor")
		if err != nil {
			t.Fatalf("metricsPortFor(supervisor): %v", err)
		}
		if port != 9500 {
			t.Errorf("metricsPortFor(supervisor) = %d, want explicit 9500 (no offset)", port)
		}
	})

	t.Run("non-numeric base is a startup-fatal error", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RIMSKY_METRICS_PORT", "ninety")
		_, err := metricsPortFor("scheduler")
		if err == nil {
			t.Fatal("non-numeric RIMSKY_METRICS_PORT should error, not silently disable metrics")
		}
		if !strings.Contains(err.Error(), "ninety") {
			t.Errorf("error %q does not name the offending value", err.Error())
		}
	})

	t.Run("non-numeric per-role override is a startup-fatal error", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("RIMSKY_METRICS_PORT_CONTROL_API", "oops")
		_, err := metricsPortFor("control-api")
		if err == nil {
			t.Fatal("non-numeric RIMSKY_METRICS_PORT_CONTROL_API should error")
		}
		if !strings.Contains(err.Error(), "RIMSKY_METRICS_PORT_CONTROL_API") || !strings.Contains(err.Error(), "oops") {
			t.Errorf("error %q should name the variable and the offending value", err.Error())
		}
	})
}

func TestMetricsHostFromEnv(t *testing.T) {
	t.Run("defaults to loopback when unset", func(t *testing.T) {
		t.Setenv("RIMSKY_METRICS_HOST", "")
		if got := metricsHostFromEnv(); got != "127.0.0.1" {
			t.Errorf("metricsHostFromEnv() = %q, want 127.0.0.1", got)
		}
	})
	t.Run("honors an explicit host", func(t *testing.T) {
		t.Setenv("RIMSKY_METRICS_HOST", "0.0.0.0")
		if got := metricsHostFromEnv(); got != "0.0.0.0" {
			t.Errorf("metricsHostFromEnv() = %q, want 0.0.0.0", got)
		}
	})
}

func TestStartMetricsServer_BindFailureReportsAndReturnsNil(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	reporter := newFailureReporter(1)
	srv := startMetricsServer("127.0.0.1", "scheduler", port, observability.NewMetricsRegistry(), shared.NewSlogLogger(testLogger(t)), reporter)
	if srv != nil {
		t.Fatalf("startMetricsServer returned non-nil server on a bind failure: %+v", srv)
	}

	select {
	case err := <-reporter.ch:
		if !strings.Contains(err.Error(), "metrics endpoint bind") {
			t.Errorf("reported error %q does not describe a bind failure", err.Error())
		}
	default:
		t.Fatal("bind failure was not reported on the fail channel")
	}
}

func TestStartMetricsServer_ServeErrorReportsAsync(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	reporter := newFailureReporter(1)
	srv := serveMetrics(ln, "scheduler", observability.NewMetricsRegistry(), shared.NewSlogLogger(testLogger(t)), reporter)
	if srv == nil {
		t.Fatal("serveMetrics returned nil for a successfully-opened listener")
	}

	err = <-reporter.ch
	if err == nil {
		t.Fatal("expected the async Serve error to be reported")
	}
	if !strings.Contains(err.Error(), "metrics endpoint") {
		t.Errorf("reported error %q does not describe the metrics endpoint serve failure", err.Error())
	}
}
