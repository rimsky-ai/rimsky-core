// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package egress

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGuardBlocksNonPublicByDefault(t *testing.T) {
	g := Guard{}
	blocked := []string{
		"127.0.0.1:80",
		"169.254.169.254:80",
		"10.0.0.5:443",
		"192.168.1.1:80",
		"172.16.0.1:80",
		"[::1]:80",
		"0.0.0.0:80",
		"[fd00::1]:80",
		"[fe80::1]:80",
		"[::ffff:127.0.0.1]:80",
	}
	for _, addr := range blocked {
		if err := g.CheckAddr(addr); err == nil {
			t.Errorf("expected %s to be blocked by default", addr)
		}
	}
}

func TestGuardAllowsPublic(t *testing.T) {
	g := Guard{}
	for _, addr := range []string{"1.1.1.1:443", "8.8.8.8:53", "[2606:4700:4700::1111]:443"} {
		if err := g.CheckAddr(addr); err != nil {
			t.Errorf("expected %s allowed, got %v", addr, err)
		}
	}
}

func TestGuardAllowlistException(t *testing.T) {
	g, err := NewGuard([]string{"10.0.0.0/8", "169.254.169.254"})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"10.1.2.3:80", "169.254.169.254:80"} {
		if err := g.CheckAddr(addr); err != nil {
			t.Errorf("allowlisted %s should pass, got %v", addr, err)
		}
	}
	if err := g.CheckAddr("192.168.0.1:80"); err == nil {
		t.Error("192.168.0.1 is not allowlisted; expected block")
	}
}

func TestNewGuardRejectsMalformed(t *testing.T) {
	if _, err := NewGuard([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for malformed allowlist entry")
	}
}

func TestHTTPClientRejectsNonHTTPScheme(t *testing.T) {
	c := Guard{}.HTTPClient(time.Second)
	req, err := http.NewRequest(http.MethodGet, "file:///etc/passwd", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(req); err == nil {
		t.Fatal("expected non-http scheme to be rejected")
	}
}

func TestHTTPClientStripsSecretHeadersOnCrossOriginRedirect(t *testing.T) {
	var gotAPIKey, gotAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/dest", http.StatusFound)
	}))
	defer origin.Close()

	g, err := NewGuard([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	c := g.HTTPClient(5 * time.Second)
	req, err := http.NewRequest(http.MethodGet, origin.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", "top-secret")
	req.Header.Set("Authorization", "Bearer top-secret")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotAPIKey != "" {
		t.Errorf("cross-origin redirect target received X-Api-Key %q, want stripped", gotAPIKey)
	}
	if gotAuth != "" {
		t.Errorf("cross-origin redirect target received Authorization %q, want stripped", gotAuth)
	}
}

func TestHTTPClientKeepsHeadersOnSameOriginRedirect(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/dest", http.StatusFound)
			return
		}
		gotAPIKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g, err := NewGuard([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	c := g.HTTPClient(5 * time.Second)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", "same-origin-key")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotAPIKey != "same-origin-key" {
		t.Errorf("same-origin redirect target got X-Api-Key %q, want preserved", gotAPIKey)
	}
}
