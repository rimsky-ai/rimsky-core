// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func dryRunServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dry_run": true,
			"would_have_created_tag": map[string]any{
				"tag":         "release",
				"template_id": "sha256-z",
			},
		})
	}))
}

// @decision: auth-dry-run-request-flag
func TestClientDecodeRecognisesTheDryRunEnvelope(t *testing.T) {
	srv := dryRunServer(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.CreateTag(context.Background(), CreateTagRequest{Tag: "release", Template: "sha256-z"})
	var preview *DryRunPreview
	if !errors.As(err, &preview) {
		t.Fatalf("a dry-run preview must reach the verb as a preview, not as a completed write: got %v", err)
	}
	if preview.Intent != "would_have_created_tag" {
		t.Fatalf("preview intent = %q, want would_have_created_tag", preview.Intent)
	}
	if preview.Details["tag"] != "release" {
		t.Fatalf("preview details = %v, want the tag the write would have created", preview.Details)
	}
	if preview.Error() != "would have created tag" {
		t.Fatalf("preview message = %q, want %q", preview.Error(), "would have created tag")
	}
}

// @decision: auth-dry-run-request-flag
func TestReportDryRunPreviewExitsZeroAndPrintsThePreview(t *testing.T) {
	preview := &DryRunPreview{
		Intent:  "would_have_created_tag",
		Details: map[string]any{"tag": "release"},
		Body:    map[string]any{"dry_run": true},
	}

	var code int
	_ = captureStdout(t, func() { code = reportError(preview) })
	if code != 0 {
		t.Fatalf("a preview exits %d, want 0: nothing was written, so nothing failed", code)
	}

	var reported bool
	out := captureStdout(t, func() { code, reported = reportDryRunPreviewAs(FormatHuman, preview) })
	if code != 0 || !reported {
		t.Fatalf("human report = (%d, %t), want (0, true)", code, reported)
	}
	if !strings.Contains(out, "would have created tag") || !strings.Contains(out, "release") {
		t.Fatalf("human output = %q, want the would-have line and the details", out)
	}

	jsonOut := captureStdout(t, func() { code, reported = reportDryRunPreviewAs(FormatJSON, preview) })
	if code != 0 || !reported {
		t.Fatalf("json report = (%d, %t), want (0, true)", code, reported)
	}
	if !strings.Contains(jsonOut, "dry_run") {
		t.Fatalf("structured output = %q, want the dry-run marker", jsonOut)
	}
}
