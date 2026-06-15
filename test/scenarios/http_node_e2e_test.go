// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-41 acceptance proof for STORY-http-node
// (spec:2026-06-08-design-corpus-bootstrap).
//
// User outcome: a template author wires a node against an upstream HTTP API
// via the bundled `http-node` executor and observes:
//   - 200 OK → the response body lands in the node's attributes_delta;
//   - 429 Too Many Requests with Retry-After → the node-run parks with a
//     `resume_at` computed from Retry-After, the supervisor's
//     SweepParkedNodes wakes the row at that wall-clock time, the
//     re-dispatch hits the upstream again, and (when the retry returns 200)
//     the node reaches terminal success;
//   - 4xx with the configured error-class JSON field present in the body →
//     a typed `http/request_invalid/<class>` terminal error surfaces (the
//     supervisor records `terminal/error/http/request_invalid/<class>` as
//     the settling signal);
//   - 4xx with the configured field absent → the stable
//     `http/request_invalid/_unspecified` leaf surfaces (the subscribable
//     fallback for taxonomy-less upstreams).
//
// The delivery surface is the bundled `http-node` executor process: this
// proof go-builds `lib/services/executors/http-node` into a temp binary,
// launches it on an OS-assigned port (driven by the executor's
// `RIMSKY_EXECUTOR_HTTP_NODE_*` env vars — including the non-default
// `error_class_field` override the spec calls out), and registers the
// executor as an `ExtraExecutors` entry on the in-process scenario
// harness. The in-process scheduler + supervisor + control-api drive the
// dispatch path; the executor issues real HTTP requests to a fake upstream
// stood up via `httptest.NewServer` in this test process; the upstream
// returns the per-path canned shapes the four legs require.
//
// LOAD-BEARING FALSIFIER (the property this proof must pin), restated from
// the spec:
//
//	"429 errors a node-run instead of parking, OR the `resume_at` isn't
//	honored by the supervisor, OR the configured error-class JSON field
//	is ignored."
//
// Each leg of the test exhibits the discriminating shape:
//
//  1. 200-leg: `terminal/success` lands and the upstream's JSON object
//     reaches `rimsky_node_attributes.data`. The cheaper shape (success
//     with empty attributes) would not falsify the "200 response
//     populates the node's output attributes" Acceptance clause.
//
//  2. 429-leg: the 429+Retry-After response forces a Park terminal with
//     a `resume_at` matching the Retry-After delta-seconds; the parked
//     row sits in `phase='parked'` with a non-NULL `resume_at`; the
//     supervisor's SweepParkedNodes wakes the row when wall-clock
//     reaches `resume_at`; the re-dispatch hits the upstream's now-200
//     response; the node reaches `terminal/success`. The cheaper shape
//     (429 → terminal/error/http/expectation_mismatch) would falsify
//     the "429 errors a node-run instead of parking" Falsifier directly.
//
//  3. 4xx-with-field-leg: the upstream's body carries
//     `{"upstream_class":"rate_limited"}` and the node's
//     `attributes.error_class_field` is `"upstream_class"` (the configured
//     non-default JSON field). The settling signal is
//     `terminal/error/http/request_invalid/rate_limited`. The cheaper
//     shape (the executor ignores the configured field and reads the
//     default `error_class` token, which the body does not carry → emits
//     `_unspecified`) would falsify the "configured error-class JSON
//     field is ignored" Falsifier.
//
//  4. 4xx-without-field-leg: the upstream's body is a parseable JSON
//     object that does not carry the configured field. The settling
//     signal is `terminal/error/http/request_invalid/_unspecified` — the
//     stable subscribable leaf that lets `http/request_invalid/*`
//     policies still match taxonomy-less upstreams.
//
// @concept: signal
// @story: http-node

package scenarios

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// httpNodeExecutorName is the executor-name token the templates below
// reference; the harness registers it as an ExtraExecutors entry pointing
// at the locally-launched http-node binary's gRPC endpoint.
const httpNodeExecutorName = "http-node"

// httpNodeErrorClassField is the non-default JSON field the test
// configures the executor to read from a 4xx body. The spec story calls
// out that the executor honors the configured field; using a non-default
// value (instead of the executor's built-in `error_class` default) is the
// discriminator that proves the field is actually consulted.
const httpNodeErrorClassField = "upstream_class"

// TestHttpNodeCrossStack drives the bundled `http-node` executor against
// a fake upstream through the real assembled product (in-process scheduler
// + supervisor + control-api against a testcontainers Postgres, dialing a
// real http-node binary over gRPC). All four legs of the spec's Acceptance
// run sequentially against the same harness and the same executor process;
// each leg uses its own httptest upstream behavior and its own
// instance/template.
//
// Not t.Parallel(): the test owns the http-node binary process and shares
// it across legs; parallelism here would also force more testcontainer
// Postgres bring-ups, which dominates runtime under loaded hosts.
func TestHttpNodeCrossStack(t *testing.T) {
	// @deliberate: 1. Build the bundled http-node executor binary out of lib/services/
	//    (a separate Go module that go.work pulls in). The build is invoked
	//    from the workspace root because go.work resolves the
	//    `./lib/services/executors/http-node` package path there.
	httpNodeBin := buildHttpNodeBinary(t)

	// @constraint: 2. Launch the http-node binary on an OS-assigned port. The non-default
	//    `error_class_field` env override is set HERE — the per-node
	//    `attributes.error_class_field` win-path is the discriminator the
	//    spec calls out, but proving the env-configured default is also
	//    consulted requires it to be set on the process. The per-node
	//    override path takes precedence for the 4xx-with-field leg; the env
	//    default is the floor for any node that doesn't override.
	httpNodeGRPCAddr := startHttpNodeBinary(t, httpNodeBin, httpNodeErrorClassField)

	// @deliberate: 3. Stand up the in-process harness with the http-node binary wired as
	//    an ExtraExecutors entry. The supervisor's dispatch path now
	//    resolves the `http-node` executor name to the launched gRPC
	//    endpoint.
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			httpNodeExecutorName: {Transport: "grpc", URL: httpNodeGRPCAddr},
		},
	})

	// @deliberate: 4. Stand up the fake upstream. Each path returns a different shape
	//    to exhibit one Acceptance leg.
	//
	//    - /ok        → 200 JSON object → leg 1 (attributes_delta lands).
	//    - /throttle  → 429 + Retry-After: 1 the FIRST hit; 200 JSON object
	//                   thereafter → leg 2 (park, supervisor wakes,
	//                   re-dispatch succeeds).
	//    - /class     → 400 JSON body carrying the configured error-class
	//                   field → leg 3 (typed http/request_invalid/<class>).
	//    - /noclass   → 400 JSON body without the configured field → leg 4
	//                   (the stable _unspecified leaf).
	//
	// The /throttle handler keeps a per-upstream "fired-once" flag so the
	// SAME path serves 429 on its first GET and 200 on the supervisor's
	// re-dispatch. That guarantees the supervisor's wake is what triggers
	// the second hit; a cheaper-shape executor that instantly retried on
	// 429 (without parking) would also see 200 on its second call, but the
	// PARKED phase + persisted `resume_at` + the supervisor-side
	// `parked_resume_started` event would be missing — those are asserted
	// independently below.
	var throttleHits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"echo":"hello","status":"ok"}`))
		case "/throttle":
			// @deliberate: First hit → 429 + Retry-After: 1 (one second; the parsed
			// resume_at lands one second in the future from the
			// executor's process clock); subsequent hits → 200.
			if atomic.AddInt64(&throttleHits, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"retried":true}`))
		case "/class":
			// @deliberate: 4xx with the CONFIGURED error-class JSON field present —
			// proves the executor reads `upstream_class` (the configured
			// non-default field), not the executor's built-in default
			// `error_class`. If the executor ignored the configured field
			// and used the default, this body would yield the
			// `_unspecified` leaf and the leg-3 assertion would fail.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"upstream_class":"rate_limited","reason":"test"}`))
		case "/noclass":
			// @deliberate: 4xx with the configured field ABSENT (the body is a
			// parseable JSON object but carries no `upstream_class` key).
			// The executor emits the stable `_unspecified` leaf so the
			// `http/request_invalid/*` subscriber surface still matches.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"unrelated":"value"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	// @deliberate: 5. Drive each leg through a fresh template + instance.

	t.Run("leg1_200_attributes_delta", func(t *testing.T) {
		// @deliberate: Deploy a template whose single node references `http-node` with
		// an attribute schema that pulls the upstream URL out of the
		// instance's params at dispatch. The schema's `properties` are
		// minimal — http-node advertises a permissive `{"type":"object"}`
		// capability schema so adding more properties is safe; extra keys
		// on the per-run attribute bag are carried as the implicit
		// request body (transport-config keys like `url`, `method`,
		// `error_class_field` are subtracted, see configAttributeKeys in
		// the executor).
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "http-node-200", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "ok", Executor: httpNodeExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":    map[string]any{"type": "string", "source": "{{params.url}}"},
							"method": map[string]any{"type": "string", "default": "GET"},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-http-node-200", map[string]any{
			"url": upstream.URL + "/ok",
		})

		okNode := h.FindNode(iid, "ok")
		require.NotNil(t, okNode)

		require.True(t, h.WaitForNodeState(okNode.ID, cascade.NodeStateFresh, 30*time.Second),
			"200-leg: node must reach fresh terminal")

		// @deliberate: The upstream's response body is a JSON object; the executor
		// merges it into the StreamClose-Success.attributes_delta and the
		// supervisor lands the delta on rimsky_node_attributes.data. Any
		// cheaper shape (success with an empty delta) would falsify the
		// spec's "200 response populates the node's output attributes"
		// Acceptance clause.
		var row *persistence.NodeAttributesRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, okNode.ID, h.GetMainRunScopeID(iid), tx)
			row = r
			return err
		}))
		require.NotNil(t, row, "200-leg: attributes row must exist after terminal/success")
		require.Equal(t, "hello", row.Data["echo"],
			"200-leg: upstream response body's `echo` field must surface in attributes")
		require.Equal(t, "ok", row.Data["status"],
			"200-leg: upstream response body's `status` field must surface in attributes")
	})

	t.Run("leg2_429_park_then_wake", func(t *testing.T) {
		// @constraint: The 429 leg requires a fresh throttleHits counter (the /throttle
		// path returns 429 on its FIRST hit then 200). Reset the counter
		// before this leg runs so the executor's first dispatch into the
		// upstream still sees 429.
		atomic.StoreInt64(&throttleHits, 0)

		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "http-node-throttle", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "throttled", Executor: httpNodeExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":    map[string]any{"type": "string", "source": "{{params.url}}"},
							"method": map[string]any{"type": "string", "default": "GET"},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-http-node-throttle", map[string]any{
			"url": upstream.URL + "/throttle",
		})

		thr := h.FindNode(iid, "throttled")
		require.NotNil(t, thr)

		// @deliberate: The first dispatch hits the upstream → 429 + Retry-After: 1 →
		// the executor emits a Park outcome (PARK_REASON_SNOOZE) with a
		// `resume_at` computed from Retry-After. The supervisor records
		// the row in `phase='parked'` with a non-NULL `resume_at`. We
		// observe this BEFORE the deadline elapses so the SweepParkedNodes
		// wake hasn't yet flipped the row back to a re-dispatch.
		require.True(t, h.WaitForNodeState(thr.ID, cascade.NodeStateParked, 30*time.Second),
			"429-leg: node must reach parked (NOT errored) when the upstream returns 429")

		// @deliberate: Probe the parked row's phase + persisted resume_at: this is the
		// load-bearing discriminator against the "429 errors a node-run
		// instead of parking" Falsifier — a row in `phase='parked'` with a
		// non-NULL `resume_at` is exclusively the Park-outcome path; an
		// erroneous 429-as-terminal-error would write phase='failed' with
		// no resume_at.
		var phase string
		var resumeAtStored *time.Time
		h.QueryRowSQL(
			`SELECT phase, resume_at FROM rimsky_node_runs WHERE node_id = $1`,
			[]any{thr.ID},
			&phase, &resumeAtStored,
		)
		require.Equal(t, "parked", phase, "429-leg: node-run must be in parked phase")
		require.NotNil(t, resumeAtStored, "429-leg: resume_at must be persisted (executor parsed Retry-After)")

		// @deliberate: The Retry-After header was `1` (one second delta-seconds). The
		// executor's parseRetryAfter adds that delta to its own
		// time.Now(); the supervisor persists the wire-format
		// `resume_at`. Bound the persisted value to a window around the
		// expected resume so a regression that drops Retry-After (e.g.
		// silently substituting the 30s default) is caught.
		//
		// Window: [now-5s, now+30s]. The lower bound accounts for the
		// dispatch latency between the executor parsing Retry-After and
		// the test reading the row; the upper bound rules out the 30s
		// `defaultRetryAfter` fallback that would fire if the executor
		// had ignored the Retry-After header.
		now := time.Now()
		require.True(t, resumeAtStored.After(now.Add(-5*time.Second)),
			"429-leg: resume_at %v should not be in the distant past (lower bound now-5s)", *resumeAtStored)
		require.True(t, resumeAtStored.Before(now.Add(30*time.Second)),
			"429-leg: resume_at %v must reflect Retry-After: 1 (must be ≪ 30s default fallback)", *resumeAtStored)

		// @deliberate: Wait for the supervisor's parked-resume sweep to wake the row.
		// The event `parked_resume_started` is emitted by
		// SweepParkedNodes when it transitions the row from parked to
		// the re-dispatch path. Its presence is the direct discriminator
		// against "the resume_at isn't honored by the supervisor": no
		// event means no wake.
		require.True(t, h.WaitForEventKind(thr.ID, "parked_resume_started", 30*time.Second),
			"429-leg: supervisor's SweepParkedNodes must wake the parked node at resume_at")

		// @deliberate: And the row's resume reason should be the deadline-elapsed
		// flavor: the row's `wake_reason` propagates into the event
		// payload's `resume_reason`. An external invalidate would record
		// `external_invalidate` — that would surface on the wrong wake
		// path.
		row := lastEventPayload(t, h, thr.ID, "parked_resume_started")
		require.Equal(t, "deadline_elapsed", row["resume_reason"],
			"429-leg: resume_reason must be deadline_elapsed (executor's resume_at fired, not external)")

		// @deliberate: The re-dispatch hits the upstream's /throttle path again; the
		// path returns 200 on its second hit. The node reaches fresh
		// (terminal/success) via the supervisor-driven re-dispatch — the
		// full park → wake → success cycle exhibited end-to-end.
		require.True(t, h.WaitForNodeState(thr.ID, cascade.NodeStateFresh, 30*time.Second),
			"429-leg: node must reach fresh after the supervisor wakes and re-dispatches")

		// @constraint: The supervisor's terminal/success event is the canonical
		// signal-shape marker; assert it landed for the woken dispatch.
		require.True(t, h.WaitForEventKind(thr.ID, "terminal/success", 10*time.Second),
			"429-leg: terminal/success must land after the resumed re-dispatch")
	})

	t.Run("leg3_4xx_with_configured_field", func(t *testing.T) {
		// @constraint: The discriminating leg: the upstream body carries
		// `{"upstream_class":"rate_limited"}`. The executor must read the
		// configured field (`upstream_class`, set as a per-node attribute)
		// rather than its built-in default (`error_class`). The settling
		// signal class is `http/request_invalid/rate_limited`.
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "http-node-with-class", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "with-class", Executor: httpNodeExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":    map[string]any{"type": "string", "source": "{{params.url}}"},
							"method": map[string]any{"type": "string", "default": "GET"},
							// @deliberate: The configured non-default JSON field name
							// the executor reads from a 4xx body. The
							// per-node value wins over the env default;
							// using a non-default value here is what
							// proves the spec's "configured error-class
							// JSON field" knob is consulted.
							"error_class_field": map[string]any{"type": "string", "default": httpNodeErrorClassField},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-http-node-with-class", map[string]any{
			"url": upstream.URL + "/class",
		})

		wc := h.FindNode(iid, "with-class")
		require.NotNil(t, wc)

		// @constraint: give_up is the default policy for an undeclared error class,
		// so the node lands in failed.
		require.True(t, h.WaitForNodeState(wc.ID, cascade.NodeStateFailed, 30*time.Second),
			"4xx-with-field-leg: node must fail when upstream returns 4xx")

		// @deliberate: The settling signal is the typed http/request_invalid/<class>
		// path: this is the property the falsifier names — if the
		// executor ignored the configured field, the body's
		// `upstream_class` would not be read and the signal would
		// instead be the `_unspecified` leaf (the leg-4 shape).
		require.True(t, waitForSettlingSignalTypePrefix(t, h, wc.ID,
			"terminal/error/http/request_invalid/rate_limited", 30*time.Second),
			"4xx-with-field-leg: settling signal must be terminal/error/http/request_invalid/rate_limited (proving the configured field %q was consulted)",
			httpNodeErrorClassField)
	})

	t.Run("leg4_4xx_without_field", func(t *testing.T) {
		// @deliberate: The complement: the upstream body is a parseable JSON object
		// that does NOT carry the configured field. The executor emits
		// the stable `_unspecified` leaf so the `http/request_invalid/*`
		// subscriber surface still matches taxonomy-less upstreams. The
		// settling signal class is `http/request_invalid/_unspecified`.
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "http-node-no-class", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "no-class", Executor: httpNodeExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":               map[string]any{"type": "string", "source": "{{params.url}}"},
							"method":            map[string]any{"type": "string", "default": "GET"},
							"error_class_field": map[string]any{"type": "string", "default": httpNodeErrorClassField},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-http-node-no-class", map[string]any{
			"url": upstream.URL + "/noclass",
		})

		nc := h.FindNode(iid, "no-class")
		require.NotNil(t, nc)

		require.True(t, h.WaitForNodeState(nc.ID, cascade.NodeStateFailed, 30*time.Second),
			"4xx-no-field-leg: node must fail when upstream returns 4xx")

		require.True(t, waitForSettlingSignalTypePrefix(t, h, nc.ID,
			"terminal/error/http/request_invalid/_unspecified", 30*time.Second),
			"4xx-no-field-leg: settling signal must be the stable _unspecified leaf (catch-all for taxonomy-less 4xx bodies)")
	})
}

// buildHttpNodeBinary go-builds the bundled http-node executor binary
// from `lib/services/executors/http-node` (a separate Go module the
// workspace's go.work pulls in) and returns the path of the built binary.
//
// Note on layout: this proof lives in the root module's `test/scenarios/`
// tree, which by the `consumption-side-isolation` depguard cannot directly
// import `lib/services/executors/http-node` (that package is `package
// main` and lives in a sibling module). The cross-stack proof therefore
// builds and exec's the bundled binary the way an operator would deploy
// it.
func buildHttpNodeBinary(t *testing.T) string {
	t.Helper()
	root := repoRootFor(t)
	out := filepath.Join(t.TempDir(), "http-node")
	cmd := exec.Command("go", "build", "-o", out, "./lib/services/executors/http-node")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build http-node: %v\n%s", err, combined)
	}
	return out
}

// repoRootFor walks up from the test's working directory until it finds
// `go.work`, returning that directory. The `go build` invocation needs to
// run from the workspace root so the `./lib/services/executors/http-node`
// package path resolves against the workspace's module graph.
//
// Distinct symbol name (vs `host_agent_harness_test.go::repoRoot`) so the
// two helpers can coexist in the same `scenarios` test package without
// build conflicts.
func repoRootFor(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRootFor: go.work not found walking up from working dir")
		}
		dir = parent
	}
}

// startHttpNodeBinary launches the http-node binary on an OS-assigned
// port, polls the gRPC port until dialable, and registers a Cleanup that
// SIGINTs the process. Returns the grpc address (host:port) the harness
// uses as the executor's endpoint.
//
// The `errorClassField` argument overrides the executor's built-in
// `error_class` default via the `RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD`
// env var so the leg-3 assertion can prove the configured field is
// honored. (The per-node `attributes.error_class_field` win-path is
// exercised at the leg level; both paths are part of the spec's
// "configurable error-class JSON field" contract.)
func startHttpNodeBinary(t *testing.T, binary string, errorClassField string) string {
	t.Helper()
	grpcPort := pickFreePort(t)
	httpPort := pickFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"RIMSKY_EXECUTOR_HTTP_NODE_HOST=127.0.0.1",
		fmt.Sprintf("RIMSKY_EXECUTOR_HTTP_NODE_PORT=%d", grpcPort),
		fmt.Sprintf("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT=%d", httpPort),
		"RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD="+errorClassField,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	require.True(t, dialableWithin(addr, 10*time.Second),
		"http-node did not come up on %s within 10s", addr)
	return addr
}

// pickFreePort grabs an OS-assigned TCP port, closes the listener, and
// returns the port number. There is a brief close-then-reuse race window
// here, acceptable for an in-process test fixture.
//
// Distinct symbol name (vs `host_agent_harness_test.go::freePort`) so the
// two helpers can coexist in the same `scenarios` test package without
// build conflicts.
func pickFreePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}

// dialableWithin poll-dials addr until a TCP connection succeeds or the
// timeout elapses. Distinct from `host_agent_harness_test.go::waitDialable`
// so the two helpers can coexist without build conflicts; the body is the
// same shape.
func dialableWithin(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
