// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type MetricsRegistry struct {
	reg *prometheus.Registry

	Dispatches            *prometheus.CounterVec
	TerminalVerdicts      *prometheus.CounterVec
	Invalidates           *prometheus.CounterVec
	ClaimAcquisitions     *prometheus.CounterVec
	NamedLockAcquisitions *prometheus.CounterVec

	NodesByState    *prometheus.GaugeVec
	ParkedByReason  *prometheus.GaugeVec
	HeldFrames      prometheus.Gauge
	NodeRunsPending prometheus.Gauge

	DispatchLatencySeconds         *prometheus.HistogramVec
	ClaimAcquisitionLatencySeconds *prometheus.HistogramVec
	FrameDurationSeconds           prometheus.Histogram
	ParkedDurationOnResumeSeconds  prometheus.Histogram
}

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
			prometheus.GaugeOpts{Name: "rimsky_node_runs_pending", Help: "Count of unclaimed rimsky_node_runs rows (state='stale' and claimed_by IS NULL) awaiting dispatch."},
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

func (m *MetricsRegistry) Registry() *prometheus.Registry { return m.reg }

func MetricsHandler(m *MetricsRegistry) http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

func MountMetrics(r chi.Router, m *MetricsRegistry) {
	r.Method(http.MethodGet, "/metrics", MetricsHandler(m))
}
