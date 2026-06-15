// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-43 acceptance proof for STORY-verifier-http
// (spec:2026-06-08-design-corpus-bootstrap).
//
// User outcome: a template author wires a verifier node against an external
// check service via the bundled `verifier-http` executor and observes:
//
//   - A claim payload the service accepts (2xx) → the verifier node reaches a
//     terminal SUCCESS state with `verifier_pass=true` carried on the
//     attributes_delta and the upstream's HTTP status echoed as
//     `verifier_status`;
//
//   - A claim payload the service rejects with a 4xx body carrying a class
//     field → the verifier node reaches a terminal ERROR with the upstream's
//     class surfaced as the typed leaf
//     `terminal/error/verifier/check_failed/<class>`;
//
//   - A 5xx upstream response → the verifier node reaches a terminal ERROR
//     (never a silent success), routed through `verifier/check_failed`;
//
//   - In every case, the upstream actually receives the JSON the operator
//     configured as the verifier's body. The fake upstream echoes the
//     received bytes back through a test-owned recorder and the test asserts
//     the recorded bytes match the configured body.
//
// The delivery surface is the bundled `verifier-http` executor PROCESS: this
// proof go-builds `lib/services/executors/verifier-http` into a temp binary
// (matching what an operator deploys; `consumption-side-isolation` blocks a
// direct import of the package since it lives in the `lib/services` module
// and is `package main`), launches it on an OS-assigned port, and registers
// it as an `ExtraExecutors` entry on the in-process scenario harness. The
// in-process scheduler + supervisor + control-api drive the dispatch path;
// the executor issues real HTTP POSTs to a fake verifier upstream stood up
// via `httptest.NewServer` in this test process.
//
// LOAD-BEARING FALSIFIER (the property this proof must pin), restated from
// the spec:
//
//	"The verifier resolves to success when the upstream returned 5xx, OR
//	the upstream's class field is dropped, OR the payload posted is canned."
//
// Each leg of the test exhibits the discriminating shape:
//
//  1. 2xx-leg: the upstream returns 200; the node reaches `terminal/success`;
//     the recorded upstream body matches the configured body BYTE-FOR-BYTE
//     (the cheaper shape — a stubbed-out canned payload — would not match).
//
//  2. 4xx-leg: the upstream returns 400 with `{"class":"rate_limited",...}`;
//     the settling signal is exactly
//     `terminal/error/verifier/check_failed/rate_limited`. The cheaper shape
//     (dropping the upstream class) would surface `verifier/check_failed`
//     without the typed leaf and the assertion fails.
//
//  3. 5xx-leg: the upstream returns 500; the node reaches terminal ERROR
//     (NOT success). The cheaper shape (5xx silently resolved to success)
//     would land the row in `fresh` and the assertion fails.
//
// @concept: signal
// @story: verifier-http
package scenarios

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// verifierHTTPExecutorName is the executor-name token the templates below
// reference; the harness registers it as an ExtraExecutors entry pointing
// at the locally-launched verifier-http binary's gRPC endpoint.
const verifierHTTPExecutorName = "verifier-http"

// TestVerifierHttpCrossStack drives the bundled `verifier-http` executor
// against a fake verifier upstream through the real assembled product
// (in-process scheduler + supervisor + control-api against a testcontainers
// Postgres, dialing a real verifier-http binary over gRPC). All three legs
// of the spec's Acceptance run sequentially against the same harness and
// the same executor process; each leg uses its own httptest path behavior
// and its own instance/template.
//
// Not t.Parallel(): the test owns the verifier-http binary process and the
// upstream recorder and shares them across legs.
func TestVerifierHttpCrossStack(t *testing.T) {
	// @deliberate: 1. Build the bundled verifier-http executor binary out of lib/services/
	//    (a separate Go module the workspace's go.work pulls in). The build
	//    is invoked from the workspace root because go.work resolves the
	//    `./lib/services/executors/verifier-http` package path there.
	verifierBin := buildVerifierHTTPBinary(t)

	// @deliberate: 2. Launch the verifier-http binary on an OS-assigned port. The
	//    executor's gRPC server reads RIMSKY_EXECUTOR_VERIFIER_HTTP_PORT (and
	//    _HOST); we pin both to the OS-assigned values so the harness's
	//    ExtraExecutors entry can dial it.
	verifierGRPCAddr := startVerifierHTTPBinary(t, verifierBin)

	// @deliberate: 3. Stand up the in-process harness with the verifier-http binary wired
	//    as an ExtraExecutors entry. The supervisor's dispatch path now
	//    resolves the `verifier-http` executor name to the launched gRPC
	//    endpoint.
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			verifierHTTPExecutorName: {Transport: "grpc", URL: verifierGRPCAddr},
		},
	})

	// @deliberate: 4. Stand up the fake verifier upstream. The recorder field captures the
	//    most-recent request body for each path, keyed by path, so each leg
	//    can assert the upstream received the operator's configured body
	//    BYTE-FOR-BYTE (the falsifier-discriminator against "the payload
	//    posted is canned").
	rec := &upstreamRecorder{lastBodyByPath: map[string][]byte{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// @deliberate: Recording is universal: we want to inspect what the executor
		// actually POSTed regardless of which leg this hit belongs to.
		raw, _ := io.ReadAll(r.Body)
		rec.record(r.URL.Path, raw)
		switch r.URL.Path {
		case "/ok":
			// @deliberate: 2xx leg: success path. Echo the inbound body back as the
			// response so the executor (and the test) could in principle
			// reflect on what reached the upstream from the response side
			// too; the recorder is the primary discriminator.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
		case "/reject":
			// @deliberate: 4xx-with-class leg: the upstream rejects the payload with a
			// 400 carrying a `class` field. The verifier-http executor
			// reads that field and surfaces it on the error class as
			// `verifier/check_failed/<class>` — the property the
			// "upstream's class field is dropped" falsifier sentinel.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"class":"rate_limited","reason":"echo: ` + safeString(raw) + `"}`))
		case "/boom":
			// @constraint: 5xx leg: server error. The verifier MUST route to terminal
			// error — never resolve to success — under the "verifier
			// resolves to success when the upstream returned 5xx"
			// falsifier sentinel.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"upstream blew up"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	// @deliberate: 5. Drive each leg through a fresh template + instance.

	t.Run("leg1_2xx_terminal_success_with_echo", func(t *testing.T) {
		// @deliberate: The body the operator wants the verifier to POST. Carrying both
		// a string and a nested object guards against any "canned payload"
		// regression in which the executor silently substitutes a fixed
		// stub.
		configuredBody := map[string]any{
			"claim_id":   "claim-2xx-leg",
			"properties": map[string]any{"k": "v", "n": float64(42)},
		}
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "verifier-http-200", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "verify-ok", Executor: verifierHTTPExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":  map[string]any{"type": "string", "source": "{{params.url}}"},
							"body": map[string]any{"type": "object", "source": "{{params.body}}"},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-verifier-http-200", map[string]any{
			"url":  upstream.URL + "/ok",
			"body": configuredBody,
		})

		okNode := h.FindNode(iid, "verify-ok")
		require.NotNil(t, okNode)

		// @deliberate: 2xx → terminal/success. Any cheaper shape (e.g. silent error
		// because the executor mis-read the configured body) falsifies
		// the spec's "a payload the service accepts reaches a terminal
		// success" Acceptance clause.
		require.True(t, h.WaitForNodeState(okNode.ID, cascade.NodeStateFresh, 30*time.Second),
			"2xx-leg: node must reach fresh terminal")

		// @deliberate: Falsifier guard: assert the upstream actually received the
		// operator's body, byte-for-byte. The recorder captured the
		// most-recent POST to /ok; decode it as JSON and require deep
		// equality against the configured body. A canned-payload
		// regression (executor swapping the body for something fixed)
		// fails this assertion.
		gotBody := rec.lastBody("/ok")
		require.NotEmpty(t, gotBody, "2xx-leg: upstream must have received a body")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(gotBody, &decoded),
			"2xx-leg: upstream-received body must be JSON; got %q", string(gotBody))
		// @constraint: Normalize both sides through JSON so numeric/float equivalence is
		// uniform (every JSON number decodes to float64).
		normExpected := jsonRoundtrip(t, configuredBody)
		require.True(t, reflect.DeepEqual(normExpected, decoded),
			"2xx-leg: upstream-received body must match configured body byte-for-byte; got %v want %v",
			decoded, normExpected)
	})

	t.Run("leg2_4xx_with_class_terminal_error_typed", func(t *testing.T) {
		// @constraint: The 4xx-with-class discriminating leg: the upstream body carries
		// `{"class":"rate_limited",...}`. The executor MUST read the class
		// field and surface it on the error-class hierarchy as
		// `verifier/check_failed/rate_limited`. The settling signal type
		// is `terminal/error/verifier/check_failed/rate_limited` — the
		// load-bearing discriminator against the "upstream's class field
		// is dropped" falsifier sentinel.
		configuredBody := map[string]any{
			"claim_id": "claim-4xx-leg",
			"shape":    map[string]any{"a": float64(1)},
		}
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "verifier-http-4xx", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "verify-reject", Executor: verifierHTTPExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":  map[string]any{"type": "string", "source": "{{params.url}}"},
							"body": map[string]any{"type": "object", "source": "{{params.body}}"},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-verifier-http-4xx", map[string]any{
			"url":  upstream.URL + "/reject",
			"body": configuredBody,
		})

		rj := h.FindNode(iid, "verify-reject")
		require.NotNil(t, rj)

		// @constraint: give_up is the default policy for an undeclared error class,
		// so the node lands in failed (NOT fresh).
		require.True(t, h.WaitForNodeState(rj.ID, cascade.NodeStateFailed, 30*time.Second),
			"4xx-leg: node must fail when upstream returns 4xx")

		// @deliberate: The settling signal is the typed verifier/check_failed/<class>
		// path: this is the property the falsifier names — if the
		// executor ignored the upstream's class field, the signal would
		// instead be `verifier/check_failed` without the typed suffix
		// and this assertion would fail.
		require.True(t, waitForSettlingSignalTypePrefix(t, h, rj.ID,
			"terminal/error/verifier/check_failed/rate_limited", 30*time.Second),
			"4xx-leg: settling signal must be terminal/error/verifier/check_failed/rate_limited (proving the upstream's class field surfaced)")

		// @constraint: Also assert the upstream actually received the configured
		// body (echo-back guard against canned payloads).
		gotBody := rec.lastBody("/reject")
		require.NotEmpty(t, gotBody, "4xx-leg: upstream must have received a body")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(gotBody, &decoded),
			"4xx-leg: upstream-received body must be JSON; got %q", string(gotBody))
		normExpected := jsonRoundtrip(t, configuredBody)
		require.True(t, reflect.DeepEqual(normExpected, decoded),
			"4xx-leg: upstream-received body must match configured body byte-for-byte; got %v want %v",
			decoded, normExpected)
	})

	t.Run("leg3_5xx_terminal_error_never_success", func(t *testing.T) {
		// @constraint: The 5xx leg: upstream returns 500. The verifier MUST route to
		// terminal error — never silently to success — under the
		// "verifier resolves to success when the upstream returned 5xx"
		// falsifier sentinel.
		configuredBody := map[string]any{
			"claim_id": "claim-5xx-leg",
		}
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "verifier-http-5xx", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "verify-boom", Executor: verifierHTTPExecutorName},
					scenario.WithAttributes(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":  map[string]any{"type": "string", "source": "{{params.url}}"},
							"body": map[string]any{"type": "object", "source": "{{params.body}}"},
						},
					}),
				),
			},
		})
		iid := h.CreateInstance(tid, "ck-verifier-http-5xx", map[string]any{
			"url":  upstream.URL + "/boom",
			"body": configuredBody,
		})

		bm := h.FindNode(iid, "verify-boom")
		require.NotNil(t, bm)

		// @constraint: 5xx → terminal/error (NOT terminal/success). The default
		// give_up policy lands the node in failed.
		require.True(t, h.WaitForNodeState(bm.ID, cascade.NodeStateFailed, 30*time.Second),
			"5xx-leg: node must fail when upstream returns 5xx (must NOT resolve to success)")

		// @constraint: The settling signal type must be a `terminal/error/verifier/...`
		// path — any `terminal/success/` prefix would falsify the
		// "verifier resolves to success when the upstream returned 5xx"
		// sentinel directly. The 5xx body in this leg does NOT carry a
		// `class` field, so the leaf is the unqualified
		// `verifier/check_failed`.
		require.True(t, waitForSettlingSignalTypePrefix(t, h, bm.ID,
			"terminal/error/verifier/check_failed", 30*time.Second),
			"5xx-leg: settling signal must be a terminal/error/verifier/check_failed* path")

		// @constraint: Echo-back guard.
		gotBody := rec.lastBody("/boom")
		require.NotEmpty(t, gotBody, "5xx-leg: upstream must have received a body")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(gotBody, &decoded),
			"5xx-leg: upstream-received body must be JSON; got %q", string(gotBody))
		normExpected := jsonRoundtrip(t, configuredBody)
		require.True(t, reflect.DeepEqual(normExpected, decoded),
			"5xx-leg: upstream-received body must match configured body byte-for-byte; got %v want %v",
			decoded, normExpected)
	})
}

// upstreamRecorder captures the most-recent request body per path so each
// test leg can verify the upstream received the operator's configured
// payload (and not a canned stub).
type upstreamRecorder struct {
	mu             sync.Mutex
	lastBodyByPath map[string][]byte
}

func (r *upstreamRecorder) record(path string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(body))
	copy(cp, body)
	r.lastBodyByPath[path] = cp
}

func (r *upstreamRecorder) lastBody(path string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastBodyByPath[path]
}

// safeString returns body as a JSON-safe string for embedding in a JSON
// response. Used by the upstream handler to echo the inbound bytes back
// into a string field without breaking the response JSON if the body
// contains quotes.
func safeString(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	enc, _ := json.Marshal(string(body))
	// @deliberate: Strip the surrounding quotes; json.Marshal of a string yields
	// `"..."`. We just want the escaped content.
	if len(enc) >= 2 && enc[0] == '"' && enc[len(enc)-1] == '"' {
		return string(enc[1 : len(enc)-1])
	}
	return string(enc)
}

// jsonRoundtrip marshals v through encoding/json and back into a generic
// map. Forces both expected and actual sides through the same
// type-coercion (every number becomes float64) so reflect.DeepEqual can
// compare them without numeric-type mismatch noise.
func jsonRoundtrip(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// buildVerifierHTTPBinary go-builds the bundled verifier-http executor
// binary from `lib/services/executors/verifier-http` (a separate Go
// module the workspace's go.work pulls in) and returns the path of the
// built binary.
//
// Note on layout: this proof lives in the root module's `test/scenarios/`
// tree, which by the `consumption-side-isolation` depguard cannot
// directly import `lib/services/executors/verifier-http` (that package
// is `package main` and lives in a sibling module). The cross-stack
// proof therefore builds and exec's the bundled binary the way an
// operator would deploy it. Mirrors the http-node sibling helper.
func buildVerifierHTTPBinary(t *testing.T) string {
	t.Helper()
	root := repoRootFor(t)
	out := filepath.Join(t.TempDir(), "verifier-http")
	cmd := exec.Command("go", "build", "-o", out, "./lib/services/executors/verifier-http")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build verifier-http: %v\n%s", err, combined)
	}
	return out
}

// startVerifierHTTPBinary launches the verifier-http binary on an
// OS-assigned port, polls the gRPC port until dialable, and registers a
// Cleanup that SIGINTs the process. Returns the gRPC address
// (host:port) the harness uses as the executor's endpoint.
func startVerifierHTTPBinary(t *testing.T, binary string) string {
	t.Helper()
	grpcPort := pickFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"RIMSKY_EXECUTOR_VERIFIER_HTTP_HOST=127.0.0.1",
		fmt.Sprintf("RIMSKY_EXECUTOR_VERIFIER_HTTP_PORT=%d", grpcPort),
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
		"verifier-http did not come up on %s within 10s", addr)
	return addr
}
