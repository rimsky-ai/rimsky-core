// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMetricsHandler_Smoke(t *testing.T) {
	t.Parallel()
	m := NewMetricsRegistry()

	m.Dispatches.WithLabelValues("worker", "complete").Inc()
	m.TerminalVerdicts.WithLabelValues("complete", "").Inc()
	m.Invalidates.WithLabelValues("admin").Inc()
	m.ClaimAcquisitions.WithLabelValues("filesystem", "rw").Inc()
	m.NamedLockAcquisitions.WithLabelValues("deploy-mutex", "acquired").Inc()
	m.NodesByState.WithLabelValues("fresh").Set(0)
	m.ParkedNodes.Set(0)
	m.HeldFrames.Set(0)
	m.NodeRunsPending.Set(0)
	m.DispatchLatencySeconds.WithLabelValues("worker").Observe(0.5)
	m.ClaimAcquisitionLatencySeconds.WithLabelValues("filesystem").Observe(0.1)
	m.FrameDurationSeconds.Observe(1.5)
	m.ParkedDurationOnResumeSeconds.Observe(60)

	r := chi.NewRouter()
	MountMetrics(r, m)
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	wants := []string{
		"rimsky_dispatches_total",
		"rimsky_terminal_verdicts_total",
		"rimsky_invalidates_total",
		"rimsky_claim_acquisitions_total",
		"rimsky_named_lock_acquisitions_total",
		"rimsky_nodes_by_state",
		"rimsky_parked_nodes",
		"rimsky_held_frames",
		"rimsky_node_runs_pending",
		"rimsky_dispatch_latency_seconds",
		"rimsky_claim_acquisition_latency_seconds",
		"rimsky_frame_duration_seconds",
		"rimsky_parked_duration_on_resume_seconds",
	}
	for _, name := range wants {
		if !strings.Contains(string(body), name) {
			t.Errorf("metric %q not present in scrape", name)
		}
	}
}
