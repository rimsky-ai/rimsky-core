// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var workspaceModuleDirs = []string{".", "lib/foundation", "lib/protocols", "lib/services"}

func readManifest(t *testing.T, rel string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(findRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(src)
}

// @decision: module-split
func TestWorkspaceTiesFiveModulesWithLocalReplaces(t *testing.T) {
	work := readManifest(t, "go.work")
	for _, dir := range workspaceModuleDirs {
		want := "./" + dir
		if dir == "." {
			want = "."
		}
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(want) + `\s*$`).MatchString(work) {
			t.Errorf("go.work no longer uses module %q — the five-module workspace tie is broken", dir)
		}
	}

	requireRe := regexp.MustCompile(`(?m)^\s*(github\.com/rimsky-ai/rimsky-core(?:/[^\s]*)?)\s+v`)
	replaceRe := regexp.MustCompile(`(?m)^\s*(?:replace\s+)?(github\.com/rimsky-ai/rimsky-core(?:/[^\s]*)?)\s*=>\s*(\.\.?/[^\s]*)`)
	for _, dir := range workspaceModuleDirs {
		mod := readManifest(t, filepath.Join(dir, "go.mod"))
		replaced := map[string]bool{}
		for _, m := range replaceRe.FindAllStringSubmatch(mod, -1) {
			replaced[m[1]] = true
		}
		for _, m := range requireRe.FindAllStringSubmatch(mod, -1) {
			if !replaced[m[1]] {
				t.Errorf("%s/go.mod requires %s without a local-path replace — network resolution of a workspace sibling would break the full-checkout consumption model", dir, m[1])
			}
		}
	}
}

// @decision: postgres-pgx-v5
// @decision: sqlite-modernc-pure-go
// @decision: http-router-chi
// @decision: cron-robfig-v3
// @decision: grpc-google-official
// @decision: metrics-prometheus-client
// @decision: jcs-cyberphone
// @decision: spec-jcs-canonicalization
// @decision: testcontainers-go
func TestChosenLibraryPinsPresent(t *testing.T) {
	var all strings.Builder
	for _, dir := range workspaceModuleDirs {
		all.WriteString(readManifest(t, filepath.Join(dir, "go.mod")))
	}
	manifest := all.String()

	pins := map[string]string{
		"github.com/jackc/pgx/v5":                     "the chosen native Postgres driver",
		"modernc.org/sqlite":                          "the chosen pure-Go SQLite driver",
		"github.com/go-chi/chi/v5":                    "the chosen HTTP router",
		"github.com/robfig/cron/v3":                   "the chosen cron parser",
		"google.golang.org/grpc":                      "the official gRPC library",
		"google.golang.org/protobuf":                  "the official protobuf library",
		"github.com/prometheus/client_golang":         "the chosen metrics client",
		"github.com/cyberphone/json-canonicalization": "the chosen JCS canonicalization library",
		"github.com/testcontainers/testcontainers-go": "the chosen integration-test container helper",
	}
	for pkg, role := range pins {
		if !strings.Contains(manifest, pkg) {
			t.Errorf("no module manifest requires %s (%s) — the decided library pin is gone", pkg, role)
		}
	}
}

// @decision: logging-slog-only
func TestNoForbiddenLoggingLibraries(t *testing.T) {
	for _, dir := range workspaceModuleDirs {
		mod := readManifest(t, filepath.Join(dir, "go.mod"))
		for _, forbidden := range []string{"go.uber.org/zap", "github.com/rs/zerolog"} {
			if strings.Contains(mod, forbidden) {
				t.Errorf("%s/go.mod requires %s — logging is stdlib slog only", dir, forbidden)
			}
		}
	}
}
