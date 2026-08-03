// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: peer-auth-mtls-mutual
// @decision: peer-auth-mtls
// @concept: peer-auth

package scenarios

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func randomCAEncryptionKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate CA encryption key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestPeerAuthMTLSOneFlipMutuallyAuthenticatesTheForwardDispatchLegAndTheControlAPIPort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	rimskyAlias := harness.NextRimskyAlias()
	executorAlias := harness.NextPeerAlias("executor-http-node-mtls")

	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithPeerAuthMTLS(randomCAEncryptionKey(t)),
		harness.WithExecutor("http-node-remote", executorAlias+":9091"),
	)
	ep := h.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpRimskyLogs(t)
		}
	})

	if !strings.HasPrefix(ep.BaseURL, "https://") {
		t.Fatalf("under peer_auth: mtls the control API must be reached over TLS, got base URL %q", ep.BaseURL)
	}

	plaintext := "http://" + strings.TrimPrefix(ep.BaseURL, "https://")
	resp, err := http.Get(plaintext + "/v1/health")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("the control API port answered plaintext HTTP under peer_auth: mtls (body %q); the flip must cover the inbound listener too", body)
		}
	}

	caRootPEM := h.DeploymentCARootPEM(ctx, t)
	apiKey := ep.MintAPIKey(t, "peer-auth-mtls-one-flip")
	ep.Bearer = apiKey

	executor := harness.StartEnrollingHTTPNodeExecutor(ctx, t, netName, executorAlias, ep.InternalURL, apiKey, caRootPEM)

	assertRefusesAnUnauthenticatedClient(t, executor.HostAddr, caRootPEM)

	h.Restart(ctx, t)
	ep = h.Endpoint
	ep.Bearer = apiKey

	if mode := ep.WaitForExecutorPeerReachable(t, "http-node-remote"); mode != "required" {
		t.Fatalf("the executor entry declared no tls key, so peer_auth: mtls must have defaulted it to required; "+
			"the stack reports tls=%q — one flip means one flip", mode)
	}

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "peer-auth-mtls-one-flip",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "http-node-remote",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"stub_probe": map[string]any{"type": "boolean", "default": true},
								"stub":       map[string]any{"type": "boolean", "default": false},
							},
						},
					},
				},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-peer-auth-mtls-one-flip")
	t.Cleanup(func() {
		if t.Failed() {
			_, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/worker", "")
			t.Logf("worker node observability: %s", string(raw))
		}
	})

	waitForDispatchToFresh(t, ep, instanceID, "worker")
}

func assertRefusesAnUnauthenticatedClient(t *testing.T, addr, caRootPEM string) {
	t.Helper()
	pool, err := enroll.CAPoolFromPEM("deployment CA", []byte(caRootPEM))
	if err != nil {
		t.Fatalf("parse deployment CA root: %v", err)
	}

	certless := enroll.PinnedTLSConfig(pool)
	certless.MaxVersion = tls.VersionTLS12
	if conn, dialErr := tls.Dial("tcp", addr, certless); dialErr == nil {
		_ = conn.Close()
		t.Fatalf("the executor at %s completed a TLS handshake with a client holding no certificate — a "+
			"successful dispatch would then prove only one-way TLS, not the mutual authentication the flip promises", addr)
	}

	impostorCA, err := pki.GenerateCA(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("generate impostor CA: %v", err)
	}
	issued, err := impostorCA.IssueLeaf("key-01JIMPOSTORPEER", time.Now().Add(-time.Hour), pki.LeafTTL)
	if err != nil {
		t.Fatalf("issue impostor leaf: %v", err)
	}
	pair, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("load impostor keypair: %v", err)
	}
	impostor := enroll.PinnedTLSConfig(pool)
	impostor.MaxVersion = tls.VersionTLS12
	impostor.Certificates = []tls.Certificate{pair}
	if conn, dialErr := tls.Dial("tcp", addr, impostor); dialErr == nil {
		_ = conn.Close()
		t.Fatalf("the executor at %s accepted a client certificate signed by a CA that is not this deployment's — "+
			"the forward leg would authenticate anyone holding any certificate", addr)
	}
}
