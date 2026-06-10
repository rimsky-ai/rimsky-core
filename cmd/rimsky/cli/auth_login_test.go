// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// withStdin replaces os.Stdin with a pipe carrying input for the duration
// of the test, restoring it on cleanup. The non-terminal stdin exercises
// promptAPIKey's scriptable fallback (no terminal echo suppression).
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		_ = r.Close()
	})
}

func TestRunAuthLogin_WritesKeyToConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mode":"authenticated","active_key_count":1,"admin_count":1}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("RIMSKY_API_KEY", "")

	// Stdin: URL line (override default), then the api-key line.
	withStdin(t, srv.URL+"\nsecret-key-123\n")

	if got := RunAuthLogin(context.Background(), nil); got != 0 {
		t.Fatalf("exit %d, want 0", got)
	}

	cfg, err := LoadConfig(filepath.Join(home, ".rimsky", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		t.Fatalf("current context %q not found in %+v", cfg.CurrentContext, cfg.Contexts)
	}
	if ctx.APIKey != "secret-key-123" {
		t.Fatalf("api_key not written: %q", ctx.APIKey)
	}
	if ctx.Endpoint != srv.URL {
		t.Fatalf("endpoint not written: %q", ctx.Endpoint)
	}
}

func TestRunAuthLogin_RejectsPositionalArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := RunAuthLogin(context.Background(), []string{"some-key"}); got != 2 {
		t.Fatalf("exit %d, want 2 (no positional args allowed)", got)
	}
}

func TestRunAuthLogin_KeyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("RIMSKY_API_KEY", "")
	withStdin(t, srv.URL+"\nbad-key\n")
	if got := RunAuthLogin(context.Background(), nil); got != 1 {
		t.Fatalf("exit %d, want 1 (key rejected)", got)
	}
}

func TestRunAuthLogin_UsesDefaultEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/status" {
			_, _ = w.Write([]byte(`{"mode":"authenticated","active_key_count":1,"admin_count":1}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("RIMSKY_API_KEY", "")

	// Pre-seed a context with an endpoint so the URL prompt defaults to it.
	cfgPath := filepath.Join(home, ".rimsky", "config.yml")
	if err := SaveConfig(cfgPath, &Config{
		CurrentContext: "dev",
		Contexts:       map[string]Context{"dev": {Endpoint: srv.URL}},
	}); err != nil {
		t.Fatal(err)
	}

	// Empty URL line accepts the default; then the api-key.
	withStdin(t, "\nthe-key\n")
	if got := RunAuthLogin(context.Background(), nil); got != 0 {
		t.Fatalf("exit %d, want 0", got)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Contexts["dev"].APIKey != "the-key" {
		t.Fatalf("api_key not written to dev context: %+v", cfg.Contexts["dev"])
	}
	if cfg.Contexts["dev"].Endpoint != srv.URL {
		t.Fatalf("endpoint changed unexpectedly: %q", cfg.Contexts["dev"].Endpoint)
	}
}
