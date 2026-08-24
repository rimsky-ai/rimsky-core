// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service-auth

package config

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

func startMTLSControlAPI(t *testing.T) (string, *pki.CA) {
	t.Helper()
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	ctx := context.Background()
	db, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ca, err := ensureDeploymentCA(ctx, db.Tables(), shared.SystemClock{})
	if err != nil {
		t.Fatalf("ensureDeploymentCA: %v", err)
	}

	handle, err := StartControlAPI(ControlAPIConfig{
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		Host:         "127.0.0.1",
		Port:         0,
		ControlAPIID: controlAPIListenerPrincipal,
		ServiceAuth:  service.ServiceAuthMTLS,
	})
	if err != nil {
		t.Fatalf("StartControlAPI under mtls: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })
	return handle.Addr(), ca
}

const controlAPIListenerPrincipal = "control-api-mtls-listener"

func leafClient(t *testing.T, issuer *pki.CA, trustRoots *pki.CA, principal string) *http.Client {
	t.Helper()
	issued, err := issuer.IssueLeaf(principal, time.Now(), pki.LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		RootCAs:      trustRoots.CertPool(),
		ServerName:   controlAPIListenerPrincipal,
	}}}
}

// @story: service-auth-mtls-mutual
// @decision: service-auth-mtls
func TestControlAPIListenerServesTLSFromTheDeploymentCAUnderMTLS(t *testing.T) {
	addr, ca := startMTLSControlAPI(t)

	plaintextResp, err := (&http.Client{}).Get("http://" + addr + "/v1/health")
	if err == nil {
		defer plaintextResp.Body.Close()
		body, _ := io.ReadAll(plaintextResp.Body)
		if plaintextResp.StatusCode == http.StatusOK {
			t.Fatalf("under service_auth: mtls the control API port must not answer plaintext HTTP; the mode is one flip and the inbound listener is inside it (got 200, body %q)", body)
		}
		if !strings.Contains(string(body), "HTTPS server") {
			t.Errorf("a plaintext request to the mTLS port should be refused as such, got %d %q", plaintextResp.StatusCode, body)
		}
	}

	client := leafClient(t, ca, ca, "service-under-test")
	resp, err := client.Get("https://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("a service holding a deployment-CA leaf must reach the control API over TLS: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health over mTLS = %d, want 200", resp.StatusCode)
	}
}

// @decision: service-auth-mtls
func TestControlAPIListenerRejectsAClientCertFromAnotherCA(t *testing.T) {
	addr, ca := startMTLSControlAPI(t)

	impostor, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	client := leafClient(t, impostor, ca, "impostor")
	if _, err := client.Get("https://" + addr + "/v1/health"); err == nil {
		t.Fatal("a client cert signed by a CA that is not this deployment's must be rejected at the handshake")
	}
}

// @decision: service-auth-mtls
func TestControlAPIListenerStaysPlaintextUnderTheDefaultPosture(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	handle, err := StartControlAPI(ControlAPIConfig{
		Driver: db,
		Clock:  shared.SystemClock{},
		Logger: shared.SilentLogger{},
		Host:   "127.0.0.1",
		Port:   0,
	})
	if err != nil {
		t.Fatalf("StartControlAPI: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })

	resp, err := http.Get("http://" + handle.Addr() + "/v1/health")
	if err != nil {
		t.Fatalf("the default posture must stay zero-config plaintext: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health = %d, want 200", resp.StatusCode)
	}
}
