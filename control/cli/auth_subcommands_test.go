// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Httptest-driven unit tests for the `rimsky auth` subcommands.
//
// Per plan J3-J9 each subcommand wants at least one test asserting
// exit code, request shape, and (where applicable) error-path
// behavior. The tests here pair a stub httptest.Server with the
// real subcommand entry points (RunAuthCreateKey / RunAuthList /
// RunAuthShow / RunAuthRevoke / RunAuthRotate / RunAuthStatus); the
// smoke test in `test/smoke/cli/...` exercises the happy-path
// lifecycle against a real control-api binary.
//
// Exit-code expectations follow the (subcommand → exit) table in the
// spec section "CLI subcommands":
//
//   - flag parse error → 2
//   - non-2xx from server → 1
//   - 2xx → 0

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/control/cli"
)

// stubServer wraps httptest.NewServer with a routes map so each test
// can declare per-(method, path) responses.
type stubServer struct {
	srv     *httptest.Server
	last    *http.Request
	lastRaw []byte
}

func newStubServer(t *testing.T, handler http.HandlerFunc) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.last = r
		// Snapshot the body so the handler closure can inspect it
		// without re-reading after the response is written.
		if r.Body != nil {
			buf := make([]byte, r.ContentLength)
			if r.ContentLength > 0 {
				_, _ = r.Body.Read(buf)
			}
			s.lastRaw = buf
		}
		handler(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestAuthCreate_HappyPath(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/keys" {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"k1","name":"n1","plaintext":"rim_secret"}`))
	})
	code := cli.RunAuthCreateKey(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "operator-bearer", "--name", "n1", "--role", "admin"})
	if code != 0 {
		t.Fatalf("RunAuthCreateKey exit code: got %d want 0", code)
	}
	if stub.last == nil {
		t.Fatalf("RunAuthCreateKey did not issue a request")
	}
	if got := stub.last.Header.Get("Authorization"); got != "Bearer operator-bearer" {
		t.Errorf("Authorization header: got %q want %q", got, "Bearer operator-bearer")
	}
}

func TestAuthCreate_FlagParseError(t *testing.T) {
	// Missing required --name; subcommand should exit 2.
	code := cli.RunAuthCreateKey(context.Background(),
		[]string{"--endpoint", "http://unused", "--role", "admin"})
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --name, got %d", code)
	}
}

func TestAuthList_HappyPath(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/auth/keys") {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	})
	code := cli.RunAuthList(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "k", "--json"})
	if code != 0 {
		t.Fatalf("RunAuthList exit code: got %d want 0", code)
	}
}

func TestAuthList_Unauthorized(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
	code := cli.RunAuthList(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "bad"})
	if code != 1 {
		t.Fatalf("RunAuthList exit code on 401: got %d want 1", code)
	}
}

func TestAuthShow_NotFound(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such key"}`))
	})
	code := cli.RunAuthShow(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "k", "ghost-key"})
	if code != 1 {
		t.Fatalf("RunAuthShow exit code on 404: got %d want 1", code)
	}
}

func TestAuthRevoke_HappyPath(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "k1", "name": "n1"})
	})
	code := cli.RunAuthRevoke(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "k", "n1"})
	if code != 0 {
		t.Fatalf("RunAuthRevoke exit code: got %d want 0", code)
	}
}

func TestAuthRotate_HappyPath(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/rotate") {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"old_key_id": "ko", "new_key_id": "kn",
			"name": "n1", "plaintext": "new_secret", "revoke_at": "2030-01-01T00:00:00Z",
		})
	})
	code := cli.RunAuthRotate(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "k", "n1"})
	if code != 0 {
		t.Fatalf("RunAuthRotate exit code: got %d want 0", code)
	}
}

func TestAuthStatus_Anonymous(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mode": "anonymous", "active_key_count": 0, "admin_count": 0,
		})
	})
	code := cli.RunAuthStatus(context.Background(),
		[]string{"--endpoint", stub.srv.URL})
	if code != 0 {
		t.Fatalf("RunAuthStatus exit code: got %d want 0", code)
	}
}

func TestAuthStatus_UnauthRendersHint(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
	code := cli.RunAuthStatus(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "bad"})
	if code != 1 {
		t.Fatalf("RunAuthStatus on 401: got %d want 1", code)
	}
}
