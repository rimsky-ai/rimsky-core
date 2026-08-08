// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: peer-auth

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func (h *RimskyHandle) DeploymentCARootPEM(ctx context.Context, t testing.TB) string {
	t.Helper()
	if h.Endpoint.HostDSN == "" {
		t.Fatalf("harness: DeploymentCARootPEM needs a reachable database — bring the stack up on postgres (drop WithSQLite), " +
			"because the sqlite state file lives inside the container")
	}
	db, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: h.Endpoint.HostDSN},
	})
	if err != nil {
		t.Fatalf("harness: open stack database to read the deployment CA: %v", err)
	}
	defer func() { _ = db.Close() }()
	row, ok, err := db.Tables().DeploymentCA().Get(ctx, nil)
	if err != nil {
		t.Fatalf("harness: read deployment CA: %v", err)
	}
	if !ok {
		t.Fatalf("harness: the stack has no deployment CA row — peer_auth: mtls stands one up at startup, so its absence " +
			"means the stack did not come up under that mode")
	}
	return string(row.CACertPEM)
}

func (e RimskyEndpoint) MintAPIKey(t testing.TB, name string, actions ...string) string {
	t.Helper()
	if len(actions) == 0 {
		actions = []string{"*"}
	}
	perms := make([]map[string]any, 0, len(actions))
	for _, a := range actions {
		perms = append(perms, map[string]any{"action": a})
	}
	status, raw := e.PostJSON(t, "/v1/auth/keys", map[string]any{"name": name, "permissions": perms})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("harness: POST /v1/auth/keys (%s): %d %s", name, status, string(raw))
	}
	var resp struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("harness: decode minted key: %v: %s", err, string(raw))
	}
	if resp.Plaintext == "" {
		t.Fatalf("harness: minted key %q carries no plaintext: %s", name, string(raw))
	}
	return resp.Plaintext
}

// @concept: peer-auth
func (e RimskyEndpoint) WaitForExecutorPeerReachable(t testing.TB, name string) string {
	t.Helper()
	var last string
	for poll := 1; ; poll++ {
		status, raw, err := e.TryGetJSON("/v1/observability/executors/"+name, "")
		switch {
		case err != nil:
			last = fmt.Sprintf("transport error: %v", err)
		case status != http.StatusOK:
			last = fmt.Sprintf("HTTP %d: %s", status, string(raw))
		default:
			var resp struct {
				Peer struct {
					Reachability string `json:"reachability_status"`
					TLS          string `json:"tls"`
					LastError    string `json:"last_error"`
				} `json:"peer"`
			}
			if decErr := json.Unmarshal(raw, &resp); decErr != nil {
				t.Fatalf("harness: decode GET /v1/observability/executors/%s: %v: %s", name, decErr, string(raw))
			}
			if resp.Peer.Reachability == "reachable" {
				return resp.Peer.TLS
			}
			last = fmt.Sprintf("reachability=%s tls=%s last_error=%s", resp.Peer.Reachability, resp.Peer.TLS, resp.Peer.LastError)
		}
		if poll%40 == 0 {
			t.Logf("harness: still waiting for executor peer %q to become reachable (last observed: %s) — the helper "+
				"blocks until the capability probe succeeds; the test guard's no-progress watchdog is the only backstop", name, last)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

type EnrollingExecutor struct {
	Endpoint string
	Alias    string
	HostAddr string
}

func NextPeerAlias(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, nextAliasSuffix())
}

func StartEnrollingHTTPNodeExecutor(
	ctx context.Context,
	t testing.TB,
	networkName, alias, controlAPIURL, apiKey, caRootPEM string,
) EnrollingExecutor {
	t.Helper()
	if !strings.HasPrefix(controlAPIURL, "https://") {
		t.Fatalf("harness: StartEnrollingHTTPNodeExecutor expects the stack's internal control-API URL to be https "+
			"under peer_auth: mtls, got %q", controlAPIURL)
	}
	c, err := runWithRetry(ctx, ImageRef("rimsky-executor-http-node"),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"RIMSKY_EXECUTOR_PORT_GRPC": "9091",
			"RIMSKY_EXECUTOR_PORT_HTTP": "9092",
			"RIMSKY_EXECUTOR_STUB_MODE": "1",
			"RIMSKY_PEER_AUTH":          "mtls",
			"RIMSKY_CONTROL_API_URL":    controlAPIURL,
			"RIMSKY_API_KEY":            apiKey,
			"RIMSKY_CONTROL_API_CA":     enrolledExecutorCARootPath,
		}),
		testcontainers.WithFiles(rereadableContainerFile(caRootPEM, enrolledExecutorCARootPath, 0o644)),
		testcontainers.WithExposedPorts("9091/tcp", "9092/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9091/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start enrolling rimsky-executor-http-node: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			dumpLogsForFailure(t, "executor-http-node/mtls", c)
		}
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("harness: enrolling executor host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "9091")
	if err != nil {
		t.Fatalf("harness: enrolling executor mapped port: %v", err)
	}
	return EnrollingExecutor{
		Endpoint: alias + ":9091",
		Alias:    alias,
		HostAddr: fmt.Sprintf("%s:%s", host, mapped.Port()),
	}
}

const enrolledExecutorCARootPath = "/etc/rimsky/deployment-ca.pem"
