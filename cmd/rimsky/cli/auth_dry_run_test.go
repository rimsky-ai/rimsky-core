// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

type authWriteVerb struct {
	name   string
	intent string
	detail string
	run    func(ctx context.Context, endpoint string) int
}

func authWriteVerbs() []authWriteVerb {
	return []authWriteVerb{
		{
			name:   "create-key",
			intent: "would_have_created_key",
			detail: "operator",
			run: func(ctx context.Context, endpoint string) int {
				return cli.RunAuthCreateKey(ctx,
					[]string{"--endpoint", endpoint, "--key", "k", "--name", "operator", "--role", "admin"})
			},
		},
		{
			name:   "init",
			intent: "would_have_created_key",
			detail: "admin",
			run: func(ctx context.Context, endpoint string) int {
				return cli.RunAuthInit(ctx, []string{"--endpoint", endpoint})
			},
		},
		{
			name:   "rotate",
			intent: "would_have_rotated_key",
			detail: "operator",
			run: func(ctx context.Context, endpoint string) int {
				return cli.RunAuthRotate(ctx, []string{"--endpoint", endpoint, "--key", "k", "operator"})
			},
		},
		{
			name:   "revoke",
			intent: "would_have_revoked_key",
			detail: "operator",
			run: func(ctx context.Context, endpoint string) int {
				return cli.RunAuthRevoke(ctx, []string{"--endpoint", endpoint, "--key", "k", "--yes", "operator"})
			},
		},
	}
}

func humanAuthOutput(t *testing.T) {
	t.Helper()
	cli.SetActiveCommonFlags(&cli.CommonFlags{Format: cli.FormatHuman})
	t.Cleanup(func() { cli.SetActiveCommonFlags(nil) })
}

// @decision: auth-dry-run-request-flag
func TestAuthWriteVerbsReportADryRunPreviewAtExitZero(t *testing.T) {
	for _, verb := range authWriteVerbs() {
		t.Run(verb.name, func(t *testing.T) {
			humanAuthOutput(t)
			stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/v1/auth/status" {
					_ = json.NewEncoder(w).Encode(map[string]any{"mode": "anonymous", "active_key_count": 0})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"dry_run":   true,
					verb.intent: map[string]any{"name": verb.detail},
				})
			})

			var code int
			out := captureAuthStdout(t, func() { code = verb.run(context.Background(), stub.srv.URL) })

			if code != 0 {
				t.Fatalf("rimsky auth %s exits %d on a dry-run preview, want 0: nothing was written, so nothing failed", verb.name, code)
			}
			want := strings.ReplaceAll(verb.intent, "_", " ")
			if !strings.Contains(out, want) {
				t.Fatalf("rimsky auth %s stdout = %q, want the preview line %q", verb.name, out, want)
			}
			if !strings.Contains(out, verb.detail) {
				t.Fatalf("rimsky auth %s stdout = %q, want the preview details naming %q", verb.name, out, verb.detail)
			}
		})
	}
}

// @decision: auth-dry-run-request-flag
func TestAuthWriteVerbsReportAGenuineFailureAtExitOne(t *testing.T) {
	for _, verb := range authWriteVerbs() {
		t.Run(verb.name, func(t *testing.T) {
			humanAuthOutput(t)
			stub := newStubServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/v1/auth/status" {
					_ = json.NewEncoder(w).Encode(map[string]any{"mode": "anonymous", "active_key_count": 0})
					return
				}
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "forbidden"})
			})

			if code := verb.run(context.Background(), stub.srv.URL); code != 1 {
				t.Fatalf("rimsky auth %s exits %d on a rejected write, want 1", verb.name, code)
			}
		})
	}
}
