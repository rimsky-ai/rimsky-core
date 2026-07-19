// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const verifierHTTPExecutorName = "verifier-http"

func TestVerifierHttpCrossStack(t *testing.T) {
	verifierBin := buildVerifierHTTPBinary(t)

	verifierGRPCAddr := startVerifierHTTPBinary(t, verifierBin)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			verifierHTTPExecutorName: {Transport: "grpc", URL: verifierGRPCAddr},
		},
	})

	rec := &upstreamRecorder{lastBodyByPath: map[string][]byte{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.record(r.URL.Path, raw)
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
		case "/reject":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"class":"rate_limited","reason":"echo: ` + safeString(raw) + `"}`))
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"upstream blew up"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	t.Run("leg1_2xx_terminal_success_with_echo", func(t *testing.T) {
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

		h.WaitForNodeState(okNode.ID, cascade.NodeStateFresh)

		gotBody := rec.lastBody("/ok")
		require.NotEmpty(t, gotBody, "2xx-leg: upstream must have received a body")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(gotBody, &decoded),
			"2xx-leg: upstream-received body must be JSON; got %q", string(gotBody))
		normExpected := jsonRoundtrip(t, configuredBody)
		require.True(t, reflect.DeepEqual(normExpected, decoded),
			"2xx-leg: upstream-received body must match configured body byte-for-byte; got %v want %v",
			decoded, normExpected)
	})

	t.Run("leg2_4xx_with_class_terminal_error_typed", func(t *testing.T) {
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

		h.WaitForNodeState(rj.ID, cascade.NodeStateFailed)

		waitForSettlingSignalTypePrefix(t, h, rj.ID, "terminal/error/verifier/check_failed/rate_limited")

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

		h.WaitForNodeState(bm.ID, cascade.NodeStateFailed)

		waitForSettlingSignalTypePrefix(t, h, bm.ID, "terminal/error/verifier/check_failed")

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

func safeString(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	enc, _ := json.Marshal(string(body))
	if len(enc) >= 2 && enc[0] == '"' && enc[len(enc)-1] == '"' {
		return string(enc[1 : len(enc)-1])
	}
	return string(enc)
}

func jsonRoundtrip(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func buildVerifierHTTPBinary(t *testing.T) string {
	t.Helper()
	root := repoRootFor(t)
	out := filepath.Join(t.TempDir(), "verifier-http")
	cmd := exec.Command("go", "build", "-o", out, "./lib/services/executors/verifier-http/cmd")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build verifier-http: %v\n%s", err, combined)
	}
	return out
}

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
	waitDialable(addr)
	return addr
}
