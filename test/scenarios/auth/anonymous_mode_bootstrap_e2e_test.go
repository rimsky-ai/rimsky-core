// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: anonymous-mode-bootstrap

package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func TestAnonymousModeBootstrap(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("RIMSKY_CONTROL_API", "")
	t.Setenv("RIMSKY_API_KEY", "")

	assertAuthStatus(t, f, "", "anonymous", 0, 0)

	code, body := f.request(t, "GET", "/v1/auth/keys", "", nil)
	if code != http.StatusOK {
		t.Fatalf("anonymous GET /v1/auth/keys: got %d %+v, want 200 (anonymous mode is open)", code, body)
	}

	stdout1, stderr1, exit1 := runAuthInit(t, f.srv.URL)
	if exit1 != 0 {
		t.Fatalf("auth init (1st): exit %d, stderr=%q, want exit 0", exit1, stderr1)
	}
	plaintext := extractPlaintext(t, stdout1)
	if plaintext == "" {
		t.Fatalf("auth init (1st): stdout did not include the minted plaintext: %q", stdout1)
	}

	if strings.Contains(stderr1, plaintext) {
		t.Fatalf("auth init (1st): plaintext leaked to stderr: %q", stderr1)
	}

	code, body = f.request(t, "GET", "/v1/auth/keys", "", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /v1/auth/keys after init: got %d %+v, want 401 (anonymous mode must close on first key mint)", code, body)
	}

	code, body = f.request(t, "GET", "/v1/auth/keys", plaintext, nil)
	if code != http.StatusOK {
		t.Fatalf("bearer GET /v1/auth/keys with minted plaintext: got %d %+v, want 200 (the plaintext returned from auth init must authenticate)", code, body)
	}

	assertAuthStatus(t, f, plaintext, "authenticated", 1, 1)

	stdout2, stderr2, exit2 := runAuthInit(t, f.srv.URL)
	if exit2 == 0 {
		t.Fatalf("auth init (2nd): exit 0; expected non-zero. stdout=%q, stderr=%q. The bootstrap surface must refuse on a deployment with active keys.", stdout2, stderr2)
	}
	if strings.Contains(stdout2, "rim_") || strings.Contains(stdout2, plaintext) {
		t.Fatalf("auth init (2nd): plaintext appeared on stdout despite refused exit. stdout=%q", stdout2)
	}
	if !strings.Contains(stderr2, "already authenticated") && !strings.Contains(stderr2, "already exist") {
		t.Logf("auth init (2nd) stderr (informational): %q", stderr2)
	}

	assertAuthStatus(t, f, plaintext, "authenticated", 1, 1)
}

func runAuthInit(t *testing.T, endpoint string) (string, string, int) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	savedStdout := os.Stdout
	savedStderr := os.Stderr
	os.Stdout = stdoutW
	os.Stderr = stderrW

	outCh := make(chan []byte, 1)
	errCh := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(stdoutR)
		outCh <- buf
	}()
	go func() {
		buf, _ := io.ReadAll(stderrR)
		errCh <- buf
	}()

	exit := cli.RunAuthInit(context.Background(), []string{"--endpoint", endpoint})

	os.Stdout = savedStdout
	os.Stderr = savedStderr
	_ = stdoutW.Close()
	_ = stderrW.Close()
	return string(<-outCh), string(<-errCh), exit
}

func extractPlaintext(t *testing.T, stdout string) string {
	t.Helper()
	lines := strings.Split(stdout, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, "Save this admin key") {
			for j := i + 1; j < len(lines); j++ {
				cand := strings.TrimSpace(lines[j])
				if cand != "" {
					return cand
				}
			}
		}
	}
	return ""
}

func assertAuthStatus(t *testing.T, f *authFixture, bearer, wantMode string, wantActive, wantAdmins int) {
	t.Helper()
	code, body := f.request(t, "GET", "/v1/auth/status", bearer, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/auth/status (bearer=%q): got %d %+v, want 200", redact(bearer), code, body)
	}
	gotMode, _ := body["mode"].(string)
	if gotMode != wantMode {
		t.Fatalf("GET /v1/auth/status mode: got %q, want %q (full body=%+v)", gotMode, wantMode, body)
	}
	gotActive, _ := body["active_key_count"].(float64)
	if int(gotActive) != wantActive {
		t.Fatalf("GET /v1/auth/status active_key_count: got %d, want %d (body=%+v)", int(gotActive), wantActive, body)
	}
	gotAdmins, _ := body["admin_count"].(float64)
	if int(gotAdmins) != wantAdmins {
		t.Fatalf("GET /v1/auth/status admin_count: got %d, want %d (body=%+v)", int(gotAdmins), wantAdmins, body)
	}
	if _, err := json.Marshal(body); err != nil {
		t.Fatalf("GET /v1/auth/status body did not re-marshal: %v", err)
	}
}

func redact(bearer string) string {
	if bearer == "" {
		return ""
	}
	return "<redacted>"
}
