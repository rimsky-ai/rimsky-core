// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// metrics.go declares the rimsky-side Prometheus metric set and wires
// the /metrics endpoint into a chi router. The metric instrumentation
// hooks are call-site wrappers (Inc / Observe / Set) that production
// code in runtime, graph/scheduler, and
// control/controlapi can call without importing prometheus directly —
// keeps the operator-visible metric surface centralised.
//
// Per plan I1 + I2.

package observability

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsRegistry is a per-process registry of rimsky's Prometheus
// metric set. Created once at startup via NewMetricsRegistry and held
// for the process lifetime; metric variables are exposed both as
// fields (for instrumentation call sites) and through the registry
// passed to promhttp.HandlerFor.
//
// Naming: every metric uses the `rimsky_` prefix and stdlib units
// suffixes (`_seconds` for histograms, `_total` automatically appended
// to counters by the prometheus library).
type MetricsRegistry struct {
	reg *prometheus.Registry

	// Counters
	Dispatches        *prometheus.CounterVec
	TerminalVerdicts  *prometheus.CounterVec
	Invalidates       *prometheus.CounterVec
	ClaimAcquisitions *prometheus.CounterVec
	// NamedLockAcquisitions is the named-lock sibling of
	// ClaimAcquisitions: a separate counter family (rather than a kind
	// label on the claim family) so the existing producer/intent label
	// set stays stable while named locks remain distinguishable from
	// producer claims at a glance.
	NamedLockAcquisitions *prometheus.CounterVec
	NamedEvents           *prometheus.CounterVec

	// Gauges
	NodesByState    *prometheus.GaugeVec
	ParkedByReason  *prometheus.GaugeVec
	HeldFrames      prometheus.Gauge
	NodeRunsPending prometheus.Gauge

	// Histograms
	DispatchLatencySeconds         *prometheus.HistogramVec
	ClaimAcquisitionLatencySeconds *prometheus.HistogramVec
	FrameDurationSeconds           prometheus.Histogram
	ParkedDurationOnResumeSeconds  prometheus.Histogram
}

// NewMetricsRegistry constructs and registers the rimsky metric set
// against a fresh prometheus.Registry. Pass the returned Registry
// pointer to MetricsHandler when wiring the /metrics endpoint.
func NewMetricsRegistry() *MetricsRegistry {
	reg := prometheus.NewRegistry()
	m := &MetricsRegistry{
		reg: reg,
		Dispatches: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "rimsky_dispatches_total", Help: "Total dispatches by executor and terminal class."},
			[]string{"executor", "terminal_class"},
		),
		TerminalVerdicts: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "rimsky_terminal_verdicts_total", Help: "Terminal verdicts by class and error_class."},
			[]string{"terminal_class", "error_class"},
		),
		Invalidates: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "rimsky_invalidates_total", Help: "Invalidates fired, by source kind."},
			[]string{"source_kind"},
		),
		ClaimAcquisitions: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "rimsky_claim_acquisitions_total", Help: "Claim acquisitions by producer and intent."},
			[]string{"producer", "intent"},
		),
		NamedLockAcquisitions: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "rimsky_named_lock_acquisitions_total", Help: "Named-lock acquisitions by lock name and intent."},
			[]string{"lock_name", "intent"},
		),
		NamedEvents: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "rimsky_named_events_total", Help: "NamedEvent emissions persisted, by emitter executor and event name."},
			[]string{"executor", "event_name"},
		),
		NodesByState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "rimsky_nodes_by_state", Help: "Count of nodes in each state."},
			[]string{"state"},
		),
		ParkedByReason: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "rimsky_parked_nodes_by_reason", Help: "Count of parked nodes by parked_reason."},
			[]string{"reason"},
		),
		HeldFrames: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "rimsky_held_frames", Help: "Count of frames with at least one parked node."},
		),
		NodeRunsPending: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "rimsky_node_runs_pending", Help: "Count of rimsky_node_runs rows in pending phase awaiting dispatch."},
		),
		DispatchLatencySeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "rimsky_dispatch_latency_seconds",
				Help:    "Wall-clock latency from dispatch start to terminal, by executor.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"executor"},
		),
		ClaimAcquisitionLatencySeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "rimsky_claim_acquisition_latency_seconds",
				Help:    "Wall-clock latency of claim acquisition transaction, by producer.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"producer"},
		),
		FrameDurationSeconds: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "rimsky_frame_duration_seconds",
				Help:    "Wall-clock duration of frames from start to terminal.",
				Buckets: prometheus.DefBuckets,
			},
		),
		ParkedDurationOnResumeSeconds: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "rimsky_parked_duration_on_resume_seconds",
				Help:    "Wall-clock duration nodes spent parked (sampled at resume).",
				Buckets: prometheus.DefBuckets,
			},
		),
	}
	reg.MustRegister(
		m.Dispatches,
		m.TerminalVerdicts,
		m.Invalidates,
		m.ClaimAcquisitions,
		m.NamedLockAcquisitions,
		m.NamedEvents,
		m.NodesByState,
		m.ParkedByReason,
		m.HeldFrames,
		m.NodeRunsPending,
		m.DispatchLatencySeconds,
		m.ClaimAcquisitionLatencySeconds,
		m.FrameDurationSeconds,
		m.ParkedDurationOnResumeSeconds,
	)
	return m
}

// Registry returns the underlying prometheus.Registry for tests that
// scrape via the metrics-text format.
func (m *MetricsRegistry) Registry() *prometheus.Registry { return m.reg }

// MetricsHandler returns the http.Handler backing /metrics for the
// supplied registry. Wire under chi's mux next to /healthz, etc.
func MetricsHandler(m *MetricsRegistry) http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// MountMetrics wires GET /metrics on the supplied chi router. Call
// from each rimsky cmd binary's startup once the registry is
// constructed.
func MountMetrics(r chi.Router, m *MetricsRegistry) {
	r.Method(http.MethodGet, "/metrics", MetricsHandler(m))
}
