// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
)

// @decision: host-agent-proxy-enrollment
func startPrivateCAServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	issued, err := ca.IssueLeaf("control-api", time.Now().Add(-time.Hour), 2*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	pair, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, ca.CertPEM(), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return srv, caPath
}

func TestControlAPIClient_AnchoredVerifiesPrivateCA(t *testing.T) {
	srv, caPath := startPrivateCAServer(t)
	client, err := controlAPIHTTPClient(Config{ControlAPIURL: srv.URL, ControlAPICAPath: caPath}, 5*time.Second)
	if err != nil {
		t.Fatalf("controlAPIHTTPClient: %v", err)
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("anchored client must verify the private-CA control API: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestControlAPIClient_UnanchoredRejectsPrivateCA(t *testing.T) {
	srv, _ := startPrivateCAServer(t)
	client, err := controlAPIHTTPClient(Config{ControlAPIURL: srv.URL}, 5*time.Second)
	if err != nil {
		t.Fatalf("controlAPIHTTPClient: %v", err)
	}
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("unanchored client must fail verification against a private-CA control API")
	}
}

func TestControlAPIClient_CAWithPlaintextURLIsStartupError(t *testing.T) {
	_, caPath := startPrivateCAServer(t)
	if _, err := controlAPIHTTPClient(Config{ControlAPIURL: "http://control-api:8080", ControlAPICAPath: caPath}, 5*time.Second); err == nil {
		t.Fatal("a pinned CA over a plaintext control-API URL must refuse startup")
	}
}

func TestControlAPIClient_UnsetCAYieldsDefaultClient(t *testing.T) {
	client, err := controlAPIHTTPClient(Config{ControlAPIURL: "http://control-api:8080"}, 5*time.Second)
	if err != nil {
		t.Fatalf("controlAPIHTTPClient: %v", err)
	}
	if client.Transport != nil {
		t.Fatal("no CA configured must yield the default transport (system trust)")
	}
}
