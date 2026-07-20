// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func captureAuthStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		out <- string(buf)
	}()
	fn()
	os.Stdout = saved
	_ = w.Close()
	return <-out
}

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
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/keys" {
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
	code := cli.RunAuthCreateKey(context.Background(),
		[]string{"--endpoint", "http://unused", "--role", "admin"})
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing --name, got %d", code)
	}
}

func TestAuthList_HappyPath(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/auth/keys") {
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

func TestAuthCreate_ExpiresFlag(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"k1","name":"n1","plaintext":"rim_secret"}`))
	})
	code := cli.RunAuthCreateKey(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "operator-bearer", "--name", "n1", "--role", "admin", "--expires", "30d"})
	if code != 0 {
		t.Fatalf("RunAuthCreateKey with --expires 30d: got exit %d want 0", code)
	}
	if !strings.Contains(string(stub.lastRaw), "expires_at") {
		t.Fatalf("request body should carry expires_at, got: %s", stub.lastRaw)
	}
}

func TestAuthCreate_ExpiresFlagRejectsGarbage(t *testing.T) {
	code := cli.RunAuthCreateKey(context.Background(),
		[]string{"--endpoint", "http://unused", "--key", "k", "--name", "n1", "--role", "admin", "--expires", "not-a-duration"})
	if code != 2 {
		t.Fatalf("RunAuthCreateKey with a garbage --expires: got exit %d want 2", code)
	}
}

func TestAuthCreate_AddRemoveEndToEnd(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"k1","name":"n1","plaintext":"rim_secret"}`))
	})
	code := cli.RunAuthCreateKey(context.Background(), []string{
		"--endpoint", stub.srv.URL, "--key", "operator-bearer", "--name", "n1", "--role", "admin",
		"--add", "custom:action", "--remove", "instance:read",
	})
	if code != 0 {
		t.Fatalf("RunAuthCreateKey with --add/--remove: got exit %d want 0", code)
	}
	body := string(stub.lastRaw)
	if !strings.Contains(body, "custom:action") {
		t.Fatalf("request body should carry the --add entry, got: %s", body)
	}
	if strings.Contains(body, `"instance:read"`) {
		t.Fatalf("request body should not carry the --remove'd entry, got: %s", body)
	}
}

func TestAuthCreate_RoleFile(t *testing.T) {
	dir := t.TempDir()
	rolePath := filepath.Join(dir, "custom-role.json")
	roleJSON := `{"name":"custom","description":"test role","permissions":[{"action":"instance:read"}]}`
	if err := os.WriteFile(rolePath, []byte(roleJSON), 0o644); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"k1","name":"n1","plaintext":"rim_secret"}`))
	})
	code := cli.RunAuthCreateKey(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "operator-bearer", "--name", "n1", "--role-file", rolePath})
	if code != 0 {
		t.Fatalf("RunAuthCreateKey with --role-file: got exit %d want 0", code)
	}
	if !strings.Contains(string(stub.lastRaw), "instance:read") {
		t.Fatalf("request body should carry the role-file's permissions, got: %s", stub.lastRaw)
	}
}

func TestAuthRevoke_ForceLeaveAnonymousSetsQueryParam(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("force_leave_anonymous") != "true" {
			http.Error(w, "missing force_leave_anonymous=true", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "k1", "name": "n1"})
	})
	code := cli.RunAuthRevoke(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "k", "--force-leave-anonymous", "n1"})
	if code != 0 {
		t.Fatalf("RunAuthRevoke --force-leave-anonymous: got exit %d want 0", code)
	}
}

func TestAuthRotate_GraceFlagInBody(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"old_key_id": "ko", "new_key_id": "kn",
			"name": "n1", "plaintext": "new_secret", "revoke_at": "2030-01-01T00:00:00Z",
		})
	})
	code := cli.RunAuthRotate(context.Background(),
		[]string{"--endpoint", stub.srv.URL, "--key", "k", "--grace", "1h", "n1"})
	if code != 0 {
		t.Fatalf("RunAuthRotate --grace: got exit %d want 0", code)
	}
	if !strings.Contains(string(stub.lastRaw), `"grace":"1h"`) {
		t.Fatalf("rotate request body should carry the --grace value, got: %s", stub.lastRaw)
	}
}

func TestAuthList_HumanTableRendersRoleAndCustomLabels(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"name": "n-custom", "id": "id-custom-00000000", "created_at": "2026-01-01T00:00:00Z", "last_used_at": "",
					"permissions": []map[string]any{{"action": "definitely-not-a-bundled-action"}},
				},
			},
		})
	})
	var code int
	out := captureAuthStdout(t, func() {
		code = cli.RunAuthList(context.Background(), []string{"--endpoint", stub.srv.URL, "--key", "k"})
	})
	if code != 0 {
		t.Fatalf("RunAuthList (human table): got exit %d want 0", code)
	}
	if !strings.Contains(out, "n-custom") || !strings.Contains(out, "custom") {
		t.Fatalf("expected human table to render name and role='custom' label, got: %q", out)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "ROLE") {
		t.Fatalf("expected a header row with NAME and ROLE columns, got: %q", out)
	}
}

func TestAuthInit_HappyPath(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"mode": "anonymous", "active_key_count": 0, "admin_count": 0})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/keys":
			if r.Header.Get("Authorization") != "" {
				http.Error(w, "anonymous bootstrap must not carry an Authorization header", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"k1","name":"admin","plaintext":"rim_admin_secret"}`))
		default:
			http.Error(w, "unexpected route "+r.URL.Path, http.StatusNotFound)
		}
	})
	var code int
	out := captureAuthStdout(t, func() {
		code = cli.RunAuthInit(context.Background(), []string{"--endpoint", stub.srv.URL})
	})
	if code != 0 {
		t.Fatalf("RunAuthInit: got exit %d want 0, output: %s", code, out)
	}
	if !strings.Contains(out, "rim_admin_secret") {
		t.Fatalf("RunAuthInit should print the plaintext admin key, got: %q", out)
	}
}

func TestAuthInit_RefusesWhenAlreadyAuthenticated(t *testing.T) {
	stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/auth/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"mode": "authenticated", "active_key_count": 1, "admin_count": 1})
			return
		}
		http.Error(w, "auth init must not call POST /v1/auth/keys when already authenticated", http.StatusBadRequest)
	})
	code := cli.RunAuthInit(context.Background(), []string{"--endpoint", stub.srv.URL})
	if code != 1 {
		t.Fatalf("RunAuthInit on an already-authenticated deployment: got exit %d want 1", code)
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
