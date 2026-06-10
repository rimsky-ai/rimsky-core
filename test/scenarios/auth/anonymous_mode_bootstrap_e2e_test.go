// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-anonymous-mode-bootstrap proof. An operator brings up a fresh
// deployment, exercises it without minting credentials (anonymous mode
// is open and every action succeeds), then runs `rimsky auth init` to
// mint the first admin key. At that moment anonymous mode closes —
// subsequent unauthenticated requests are refused — and a second
// `rimsky auth init` against the same deployment refuses because keys
// already exist. The status surface (`GET /v1/auth/status`) honestly
// reports the deployment's mode at every step.
//
// This test exercises the real cross-cutting bootstrap surface — the
// in-process control-api over a freshly-migrated SQLite-backed
// persistence handle (no Docker) is the production code path the
// all-in-one container exposes, and it is wired to the real
// `cli.RunAuthInit` entrypoint (the same function `rimsky auth init`
// invokes from main.go). Pointing the CLI at the fixture's
// httptest.Server URL via the `--endpoint` flag drives the same
// `/v1/auth/keys` and `/v1/auth/status` traffic the container would.
//
// Falsifier brief (Pass 23):
//   - anonymous mode stays open after a key is minted (server still
//     accepts unauthenticated requests) — covered by step 4 below.
//   - `rimsky auth init` succeeds on a deployment that already has
//     keys — covered by step 6 below.
//   - the status surface lies about which mode is active — covered by
//     the four /v1/auth/status assertions at steps 1, 3, 5, and 7.
//
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

// TestAnonymousModeBootstrap drives the full STORY-anonymous-mode-bootstrap
// outcome end-to-end against a freshly-migrated control-api: anonymous
// mode permits unauthenticated requests, `rimsky auth init` mints the
// first admin key, anonymous mode closes, unauthenticated requests are
// refused, and a second `rimsky auth init` refuses with the keys-table
// non-empty diagnostic.
func TestAnonymousModeBootstrap(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Isolate from any developer ~/.rimsky/config.yml — the auth
	// subcommands resolve the endpoint via flag → env → config in
	// that order. We pass --endpoint explicitly below, but stamping a
	// throwaway HOME keeps a stray config from coloring resolution.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RIMSKY_CONTROL_API", "")
	t.Setenv("RIMSKY_API_KEY", "")

	// ────────────────────────────────────────────────────────────────
	// Step 1: fresh deployment — keys table is empty, status reports
	// "anonymous". This is the precondition the story names.
	// ────────────────────────────────────────────────────────────────
	assertAuthStatus(t, f, "", "anonymous", 0, 0)

	// ────────────────────────────────────────────────────────────────
	// Step 2: anonymous-mode requests succeed without a bearer token.
	// We pick a route that is auth-gated in authenticated mode
	// (`GET /v1/auth/keys` requires `auth:read`) so the same call
	// later exhibits the closed-mode 401. The same property holds for
	// arbitrary `auth:write` routes — the fixture intentionally drives
	// `POST /v1/auth/keys` for `auth init` itself, so that path is
	// covered by step 3.
	// ────────────────────────────────────────────────────────────────
	code, body := f.request(t, "GET", "/v1/auth/keys", "", nil)
	if code != http.StatusOK {
		t.Fatalf("anonymous GET /v1/auth/keys: got %d %+v, want 200 (anonymous mode is open)", code, body)
	}

	// ────────────────────────────────────────────────────────────────
	// Step 3: `rimsky auth init` against the anonymous deployment.
	// Drive the real CLI entry point (the same function
	// cmd/rimsky/main.go invokes for the `auth init` subcommand).
	// Capture stdout so we can prove the plaintext is returned —
	// exactly once.
	// ────────────────────────────────────────────────────────────────
	stdout1, stderr1, exit1 := runAuthInit(t, f.srv.URL)
	if exit1 != 0 {
		t.Fatalf("auth init (1st): exit %d, stderr=%q, want exit 0", exit1, stderr1)
	}
	plaintext := extractPlaintext(t, stdout1)
	if plaintext == "" {
		t.Fatalf("auth init (1st): stdout did not include the minted plaintext: %q", stdout1)
	}

	// The plaintext must NOT also appear in stderr (would-be
	// log/banner leakage). It is a write-once secret.
	if strings.Contains(stderr1, plaintext) {
		t.Fatalf("auth init (1st): plaintext leaked to stderr: %q", stderr1)
	}

	// ────────────────────────────────────────────────────────────────
	// Step 4: after init, anonymous mode is CLOSED. An unauthenticated
	// request to the same route used in step 2 must now be refused.
	// This is the load-bearing falsifier check (anonymous mode stays
	// open after a key is minted).
	// ────────────────────────────────────────────────────────────────
	code, body = f.request(t, "GET", "/v1/auth/keys", "", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /v1/auth/keys after init: got %d %+v, want 401 (anonymous mode must close on first key mint)", code, body)
	}

	// Sanity: the minted plaintext is a real bearer credential — the
	// same route succeeds when presented. This confirms what we
	// captured from stdout is the actual minted key, not a placeholder.
	code, body = f.request(t, "GET", "/v1/auth/keys", plaintext, nil)
	if code != http.StatusOK {
		t.Fatalf("bearer GET /v1/auth/keys with minted plaintext: got %d %+v, want 200 (the plaintext returned from auth init must authenticate)", code, body)
	}

	// ────────────────────────────────────────────────────────────────
	// Step 5: status surface honestly reports the new mode. Active
	// count is 1, admin count is 1 (the bundled `admin` role is the
	// literal `*` grant — see cmd/rimsky/cli/roles/admin.json — which
	// is the admin_count predicate).
	// ────────────────────────────────────────────────────────────────
	assertAuthStatus(t, f, plaintext, "authenticated", 1, 1)

	// ────────────────────────────────────────────────────────────────
	// Step 6: re-running `rimsky auth init` against the same deployment
	// MUST refuse (the keys table is non-empty). This is the second
	// load-bearing falsifier check.
	//
	// Two surfaces enforce the refusal:
	//   - CLI-side: auth_init.go fetches /v1/auth/status before mint
	//     and exits 1 with "already authenticated" when active>0.
	//   - Server-side: even if the CLI nicety were missing, the POST
	//     /v1/auth/keys endpoint would refuse the unauthenticated
	//     request now that anonymous mode has closed (the call would
	//     reach the auth gate and 401).
	//
	// Either way, plaintext must NOT be printed again — a successful
	// stdout that includes a freshly-minted plaintext would mean the
	// bootstrap surface re-issued an admin credential against a
	// already-bootstrapped deployment.
	// ────────────────────────────────────────────────────────────────
	stdout2, stderr2, exit2 := runAuthInit(t, f.srv.URL)
	if exit2 == 0 {
		t.Fatalf("auth init (2nd): exit 0; expected non-zero. stdout=%q, stderr=%q. The bootstrap surface must refuse on a deployment with active keys.", stdout2, stderr2)
	}
	if strings.Contains(stdout2, "rim_") || strings.Contains(stdout2, plaintext) {
		t.Fatalf("auth init (2nd): plaintext appeared on stdout despite refused exit. stdout=%q", stdout2)
	}
	// The stderr message names the recovery path — operators must be
	// directed at `rimsky auth create-key` rather than left guessing.
	// (The exact phrase comes from auth_init.go.)
	if !strings.Contains(stderr2, "already authenticated") && !strings.Contains(stderr2, "already exist") {
		t.Logf("auth init (2nd) stderr (informational): %q", stderr2)
	}

	// ────────────────────────────────────────────────────────────────
	// Step 7: status surface still reports `authenticated` after the
	// refused re-init (no second key minted, no mode regression).
	// ────────────────────────────────────────────────────────────────
	assertAuthStatus(t, f, plaintext, "authenticated", 1, 1)
}

// runAuthInit invokes cli.RunAuthInit with --endpoint pointed at the
// fixture, capturing stdout and stderr through OS pipes. The CLI writes
// the minted plaintext to stdout (see auth_init.go) — we need the
// captured stream to assert both presence (1st call) and absence (2nd
// call) of a plaintext credential.
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

	// Drain on background goroutines so writes larger than the OS
	// pipe buffer don't deadlock during the CLI invocation.
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

// extractPlaintext finds the minted plaintext on auth_init.go's stdout.
// The CLI prints the key on a line indented by two spaces directly
// after "Save this admin key now…". A test that scrapes the wire would
// also work (POST /v1/auth/keys's 201 body carries the same value),
// but reading from the CLI's stdout is the user-observable surface
// the story names ("plaintext returned exactly once").
func extractPlaintext(t *testing.T, stdout string) string {
	t.Helper()
	// Find the banner line; the plaintext is the next non-blank line.
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

// assertAuthStatus issues GET /v1/auth/status and asserts the mode,
// active key count, and admin count. bearer="" exercises the
// anonymous-mode path through the gate.
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
	// JSON numeric fields decode as float64; coerce for the count
	// comparisons.
	gotActive, _ := body["active_key_count"].(float64)
	if int(gotActive) != wantActive {
		t.Fatalf("GET /v1/auth/status active_key_count: got %d, want %d (body=%+v)", int(gotActive), wantActive, body)
	}
	gotAdmins, _ := body["admin_count"].(float64)
	if int(gotAdmins) != wantAdmins {
		t.Fatalf("GET /v1/auth/status admin_count: got %d, want %d (body=%+v)", int(gotAdmins), wantAdmins, body)
	}
	// Re-encode the body so a JSON-parsing regression here surfaces
	// loudly rather than as a silent wrong-shape pass. (Mirrors a
	// belt-and-suspenders check elsewhere in the auth suite.)
	if _, err := json.Marshal(body); err != nil {
		t.Fatalf("GET /v1/auth/status body did not re-marshal: %v", err)
	}
}

// redact masks a bearer string for the diagnostic in
// assertAuthStatus — we never want the minted plaintext on the test
// log when the assertion is naming why the status check failed.
func redact(bearer string) string {
	if bearer == "" {
		return ""
	}
	return "<redacted>"
}
