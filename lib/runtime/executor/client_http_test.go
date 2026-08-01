// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func stubBridgeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/Execute" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{
			"success": map[string]any{"changed": true},
		})
		_, _ = w.Write(body)
	})
}

func TestHTTPClientTLSRequiredVerifiedExchange(t *testing.T) {
	srv := httptest.NewTLSServer(stubBridgeHandler())
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	peer.SetTLSRootCAsForTesting(pool)
	defer peer.SetTLSRootCAsForTesting(nil)

	c, err := NewHTTPClient(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeRequired})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer c.Close()
	outcome, _, err := c.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		t.Fatalf("Execute over tls: required: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome, got %+v", outcome)
	}
}

func TestHTTPClientTLSRequiredRejectsPlaintextScheme(t *testing.T) {
	_, err := NewHTTPClient(Endpoint{Transport: "http", URL: "http://plaintext-bridge:8080", TLS: peer.TLSModeRequired})
	if err == nil {
		t.Fatal("expected NewHTTPClient to reject tls: required with an http:// URL")
	}
	if !strings.Contains(err.Error(), "http://plaintext-bridge:8080") {
		t.Fatalf("error does not name the peer endpoint: %v", err)
	}
	if !strings.Contains(err.Error(), "tls: required") {
		t.Fatalf("error does not name the configured mode: %v", err)
	}
}

func TestHTTPClientTLSRequiredUnverifiedPeerFailsLoudly(t *testing.T) {
	srv := httptest.NewTLSServer(stubBridgeHandler())
	defer srv.Close()

	c, err := NewHTTPClient(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeRequired})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer c.Close()
	_, _, err = c.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err == nil {
		t.Fatal("expected verified-TLS handshake against an untrusted cert to fail")
	}
	if !strings.Contains(err.Error(), srv.URL) || !strings.Contains(err.Error(), "tls: required") {
		t.Fatalf("handshake failure does not name the peer and mode: %v", err)
	}
}

func TestHTTPClientTLSOffPlaintext(t *testing.T) {
	srv := httptest.NewServer(stubBridgeHandler())
	defer srv.Close()

	c, err := NewHTTPClient(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeOff})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer c.Close()
	outcome, _, err := c.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		t.Fatalf("Execute over tls: off: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome, got %+v", outcome)
	}
}

func TestHTTPClientForwardsServiceNameHeader(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(peer.ServiceNameHTTPHeader)
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{"success": map[string]any{"changed": true}})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeOff})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer c.Close()

	ctx := peer.WithServiceName(context.Background(), "my-executor")
	if _, _, err := c.Execute(ctx, &genv1.ExecuteRequest{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotHeader != "my-executor" {
		t.Fatalf("service-name header = %q, want %q", gotHeader, "my-executor")
	}
}

func TestHTTPClientOmitsServiceNameHeaderWhenUnset(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get(peer.ServiceNameHTTPHeader) != ""
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{"success": map[string]any{"changed": true}})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeOff})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer c.Close()

	if _, _, err := c.Execute(context.Background(), &genv1.ExecuteRequest{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sawHeader {
		t.Fatal("expected no service-name header when ctx carries none")
	}
}

func TestHTTPClientToleratesUnknownOutcomeFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":{"changed":true},"unknown_future_field":{"x":1}}`))
	}))
	defer srv.Close()

	c, err := NewHTTPClient(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeOff})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer c.Close()

	outcome, _, err := c.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		t.Fatalf("Execute with an unknown top-level Outcome field must not fail (gRPC's binary wire format already tolerates it): %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome, got %+v", outcome)
	}
}

func TestClientPoolKeyIncludesTLSMode(t *testing.T) {
	srv := httptest.NewTLSServer(stubBridgeHandler())
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	peer.SetTLSRootCAsForTesting(pool)
	defer peer.SetTLSRootCAsForTesting(nil)

	p := NewClientPool()
	defer p.Close()
	cOff, err := p.GetOrCreate(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeOff})
	if err != nil {
		t.Fatalf("GetOrCreate(off): %v", err)
	}
	cReq, err := p.GetOrCreate(Endpoint{Transport: "http", URL: srv.URL, TLS: peer.TLSModeRequired})
	if err != nil {
		t.Fatalf("GetOrCreate(required): %v", err)
	}
	if cOff == cReq {
		t.Fatal("pool returned the same client for tls: off and tls: required entries sharing a URL")
	}
}
