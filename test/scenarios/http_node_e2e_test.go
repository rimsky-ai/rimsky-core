// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

const httpNodeExecutorName = "http-node"

const httpNodeErrorClassField = "upstream_class"

func TestHttpNodeCrossStack(t *testing.T) {
	httpNodeBin := buildHttpNodeBinary(t)

	httpNodeGRPCAddr := startHttpNodeBinary(t, httpNodeBin, httpNodeErrorClassField)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			httpNodeExecutorName: {Transport: "grpc", URL: httpNodeGRPCAddr},
		},
	})

	var throttleHits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"echo":"hello","status":"ok"}`))
		case "/throttle":
			if atomic.AddInt64(&throttleHits, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"retried":true}`))
		case "/class":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"upstream_class":"rate_limited","reason":"test"}`))
		case "/noclass":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"unrelated":"value"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	t.Run("leg1_200_attributes_delta", func(t *testing.T) {
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

		h.WaitForNodeState(okNode.ID, cascade.NodeStateFresh)

		var row *persistence.NodeAttributesRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, okNode.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
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

		h.WaitForNodeState(thr.ID, cascade.NodeStateParked)

		var phase string
		var resumeAtStored *time.Time
		h.QueryRowSQL(
			`SELECT state, resume_at FROM rimsky_node_runs WHERE node_id = $1 ORDER BY sequence DESC LIMIT 1`,
			[]any{thr.ID},
			&phase, &resumeAtStored,
		)
		require.Equal(t, "parked", phase, "429-leg: node-run must be in parked phase")
		require.NotNil(t, resumeAtStored, "429-leg: resume_at must be persisted (executor parsed Retry-After)")

		now := time.Now()
		require.True(t, resumeAtStored.After(now.Add(-5*time.Second)),
			"429-leg: resume_at %v should not be in the distant past (lower bound now-5s)", *resumeAtStored)
		require.True(t, resumeAtStored.Before(now.Add(30*time.Second)),
			"429-leg: resume_at %v must reflect Retry-After: 1 (must be ≪ 30s default fallback)", *resumeAtStored)

		h.WaitForEventCount(thr.ID, "parked_resume_started", 1)

		row := lastEventPayload(t, h, thr.ID, "parked_resume_started")
		require.Equal(t, "deadline_elapsed", row["resume_reason"],
			"429-leg: resume_reason must be deadline_elapsed (executor's resume_at fired, not external)")

		h.WaitForNodeState(thr.ID, cascade.NodeStateFresh)

		h.WaitForEventCount(thr.ID, "terminal/success", 1)
	})

	t.Run("leg3_4xx_with_configured_field", func(t *testing.T) {
		tid := h.DeployTemplate(node.TemplateSpec{
			Name: "http-node-with-class", Version: "1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "with-class", Executor: httpNodeExecutorName},
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
		iid := h.CreateInstance(tid, "ck-http-node-with-class", map[string]any{
			"url": upstream.URL + "/class",
		})

		wc := h.FindNode(iid, "with-class")
		require.NotNil(t, wc)

		h.WaitForNodeState(wc.ID, cascade.NodeStateFailed)

		waitForSettlingSignalTypePrefix(t, h, wc.ID, "terminal/error/http/request_invalid/rate_limited")
	})

	t.Run("leg4_4xx_without_field", func(t *testing.T) {
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

		h.WaitForNodeState(nc.ID, cascade.NodeStateFailed)

		waitForSettlingSignalTypePrefix(t, h, nc.ID, "terminal/error/http/request_invalid/_unspecified")
	})
}

func buildHttpNodeBinary(t *testing.T) string {
	t.Helper()
	root := repoRootFor(t)
	out := filepath.Join(t.TempDir(), "http-node")
	cmd := exec.Command("go", "build", "-o", out, "./lib/services/executors/http-node/cmd")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build http-node: %v\n%s", err, combined)
	}
	return out
}

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

func startHttpNodeBinary(t *testing.T, binary string, errorClassField string) string {
	t.Helper()
	grpcPort := pickFreePort(t)
	httpPort := pickFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"RIMSKY_EXECUTOR_HOST=127.0.0.1",
		fmt.Sprintf("RIMSKY_EXECUTOR_PORT_GRPC=%d", grpcPort),
		fmt.Sprintf("RIMSKY_EXECUTOR_PORT_HTTP=%d", httpPort),
		"RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD="+errorClassField,
		"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=127.0.0.0/8",
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
		//nolint:testwallclock-pacing a teardown grace before SIGKILL; the arm kills the child and reaches no verdict
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	waitDialable(t, addr)
	return addr
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}
