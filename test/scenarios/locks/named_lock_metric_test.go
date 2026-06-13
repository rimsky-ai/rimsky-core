// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable acceptance proof for STORY-named-lock-metric: named-lock
// acquisitions are visible on the Prometheus metrics endpoint —
// labeled, distinguishable from producer-claim acquisitions — so lock
// saturation is graphable rather than reconstructable from the events
// ledger.
//
// What this proves (and what falsifies it): the proof wires the REAL
// prometheus-backed observability.RegistryHook (the same MetricsHook
// implementation production launch code threads into the supervisor's
// RunArgs) and scrapes the REAL GET /metrics endpoint mounted by
// observability.MountMetrics. It then drives real contending node-runs
// against testcontainers Postgres — a holder acquiring a limit:1 named
// lock, a contender bailing on saturation, and the contender acquiring
// after release — and asserts the scrape shows
// rimsky_named_lock_acquisitions_total moving with the lock_name label
// and both intent labels ("acquired" / "unavailable"). If the
// acquisition path stopped incrementing the counter (the story's
// falsifier: the events ledger as the only trace), every per-series
// assertion below reads 0-or-absent and the test FAILS.
//
// Acquisition is driven exactly as named_lock_limit_test.go drives it:
// NoSupervisor=true, manual runtime.RunNode cycles, and the shared
// barrierExecutor to hold a node-run mid-flight so saturation is
// observable. This file reuses that file's barrier + harness helpers
// (same package).

package locks

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// scrapeMetrics performs a real HTTP GET against the mounted /metrics
// endpoint — the operator's delivery surface — and returns the
// text-format body.
func scrapeMetrics(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url + "/metrics")
	require.NoError(t, err, "GET /metrics")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "/metrics status")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read /metrics body")
	return string(body)
}

// counterValue extracts one labeled counter sample from a Prometheus
// text-format scrape. Returns 0 when the series is absent (a counter
// that never moved emits no per-label series).
func counterValue(t *testing.T, scrape, family, lockName, intent string) float64 {
	t.Helper()
	// Text format emits labels alphabetically: intent before lock_name.
	re := regexp.MustCompile(
		fmt.Sprintf(`(?m)^%s\{intent="%s",lock_name="%s"\} ([0-9.e+]+)$`,
			regexp.QuoteMeta(family), regexp.QuoteMeta(intent), regexp.QuoteMeta(lockName)))
	m := re.FindStringSubmatch(scrape)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	require.NoError(t, err, "parse counter sample %q", m[1])
	return v
}

// TestNamedLockAcquisitionMovesLabeledMetric is the STORY-named-lock-metric
// acceptance proof. It drives named-lock acquisitions through the real
// runner with the real prometheus-backed hook and asserts the operator-
// visible counter movement and labeling on the scraped /metrics endpoint.
func TestNamedLockAcquisitionMovesLabeledMetric(t *testing.T) {
	t.Parallel()

	const (
		lockName = "metric-lock"
		family   = "rimsky_named_lock_acquisitions_total"
	)
	barrier := newBarrierExecutor()
	execAddr := startBarrierExecutor(t, barrier)

	namedLocks := locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{
		lockName: {Limit: 1},
	}}

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NamedLocks:   namedLocks,
		ExtraExecutors: map[string]executor.Endpoint{
			"barrier": {Transport: "grpc", URL: execAddr},
		},
	})

	// The real metric plumbing: prometheus registry → RegistryHook
	// (production MetricsHook implementation) → /metrics over HTTP via
	// the production MountMetrics wiring. No stub hook, no in-process
	// shortcut around the scrape surface.
	reg := observability.NewMetricsRegistry()
	hook := observability.MetricsHookOf(reg)
	mux := chi.NewRouter()
	observability.MountMetrics(mux, reg)
	metricsSrv := httptest.NewServer(mux)
	t.Cleanup(metricsSrv.Close)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-lock-metric", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "barrier"},
				scenario.WithLocks(scenario.MutexLock(lockName)),
			),
		},
	})
	iidA := h.CreateInstance(tid, "ck-named-metric-A", map[string]any{})
	iidB := h.CreateInstance(tid, "ck-named-metric-B", map[string]any{})
	wA := h.FindNode(iidA, "worker")
	wB := h.FindNode(iidB, "worker")
	require.NotNil(t, wA)
	require.NotNil(t, wB)
	require.True(t, h.WaitForDispatch(wA.ID, 15*time.Second), "wA dispatch row never appeared")
	require.True(t, h.WaitForDispatch(wB.ID, 15*time.Second), "wB dispatch row never appeared")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	runArgs := func(supID string) runtime.RunArgs {
		args := makeNamedLockRunArgs(h, supID, execAddr, namedLocks, pool)
		args.Metrics = hook
		return args
	}

	// Baseline: before any acquisition the named-lock family carries no
	// sample for this lock.
	scrape0 := scrapeMetrics(t, metricsSrv.URL)
	require.Zero(t, counterValue(t, scrape0, family, lockName, "acquired"),
		"baseline: no acquired sample before any RunNode")
	require.Zero(t, counterValue(t, scrape0, family, lockName, "unavailable"),
		"baseline: no unavailable sample before any RunNode")

	// Holder-1 acquires the mutex and parks in the barrier executor —
	// the acquisition has committed (entered fired), so the counter has
	// moved by the time we scrape.
	holder1 := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, runArgs("sup-metric-1"), nil)
		holder1 <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)

	scrape1 := scrapeMetrics(t, metricsSrv.URL)
	require.Equal(t, 1.0, counterValue(t, scrape1, family, lockName, "acquired"),
		"COUNTER MOVEMENT: one named-lock acquisition must read 1 on the metrics endpoint (story falsifier: ledger-only trace)")

	// Contender bails on the saturated mutex → the "unavailable" intent
	// label moves, distinguishable from "acquired".
	bail := mustRunNodeWithArgs(t, h, runArgs("sup-metric-2"))
	require.NoError(t, bail.err, "saturation is a soft bail, not an error")
	require.False(t, bail.out.Ran, "contender must bail while holder-1 holds the limit:1 lock")

	scrape2 := scrapeMetrics(t, metricsSrv.URL)
	require.Equal(t, 1.0, counterValue(t, scrape2, family, lockName, "acquired"),
		"acquired stays at 1 after the bail")
	require.Equal(t, 1.0, counterValue(t, scrape2, family, lockName, "unavailable"),
		"LABELING: the saturation bail must move the unavailable-intent series")

	// LABELING vs producer claims: the named-lock series lives in its
	// own family — the producer-claim family must NOT carry the lock
	// name as a producer label. This is the "distinguishable from
	// producer-claim acquisitions" half of the acceptance.
	require.NotRegexp(t,
		regexp.MustCompile(`rimsky_claim_acquisitions_total\{[^}]*"`+regexp.QuoteMeta(lockName)+`"`),
		scrape2,
		"named-lock acquisitions must not masquerade as producer-claim samples")

	// Release holder-1 → terminal → contender acquires the freed mutex
	// → counter climbs to 2 under load, the operator-observable motion.
	barrier.freeOne()
	r1 := <-holder1
	require.NoError(t, r1.err, "holder-1 RunNode error")
	require.True(t, r1.out.Ran, "holder-1 must have run")
	requireNamedLockCountEventually(t, h, lockName, 0, 5*time.Second)

	holder2 := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, runArgs("sup-metric-2"), nil)
		holder2 <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)
	barrier.freeOne()
	r2 := <-holder2
	require.NoError(t, r2.err, "holder-2 RunNode error")
	require.True(t, r2.out.Ran, "holder-2 must acquire after release")

	scrape3 := scrapeMetrics(t, metricsSrv.URL)
	require.Equal(t, 2.0, counterValue(t, scrape3, family, lockName, "acquired"),
		"MOVEMENT UNDER LOAD: the second acquisition climbs the acquired series to 2")
	require.Equal(t, 1.0, counterValue(t, scrape3, family, lockName, "unavailable"),
		"unavailable stays at 1 — the intents move independently")
}

// mustRunNodeWithArgs runs one RunNode cycle synchronously with the
// caller-supplied RunArgs (metrics hook included) and returns the result.
//
// @source: test/scenarios/locks/named_lock_limit_test.go:mustRunNode
// @diverged: true
// @reason: takes pre-built RunArgs so the metrics hook threads through.
func mustRunNodeWithArgs(t *testing.T, h *scenario.Harness, args runtime.RunArgs) runResult {
	t.Helper()
	out, err := runtime.RunNode(h.Ctx, args, nil)
	return runResult{out: out, err: err}
}
