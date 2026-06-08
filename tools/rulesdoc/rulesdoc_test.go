// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package rulesdoc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRulesDoc_CitedPathsExist is the doc-drift accuracy gate for
// .claude/rules/rules.md (story S-cli-onboarding-rules-deploy-paths). It scans
// the rules document for every repo-relative filesystem path it instructs a
// contributor to use and asserts each resolves against the current tree, then
// pins the specific positive/negative contract: the rebuild instruction must
// name the real mechanism (`make core-images`) and must carry NONE of the
// currently-dead references (the absent deploy/ paths, the bare
// executors/claude-agent prefix that the tree relocated under lib/services/,
// and the relocated stores-redesign sketch path).
//
// Today this test FAILS: rules.md cites deploy/build-images.sh and
// deploy/docker-compose.yml (no deploy/ directory exists), the bare
// executors/claude-agent prefix (the real tree is lib/services/executors/...),
// and docs/2026-04-25-stores-redesign.md (no docs/ directory), and it does not
// yet name `make core-images`. A later GREEN pass corrects rules.md; this pass
// only authors the failing gate.
func TestRulesDoc_CitedPathsExist(t *testing.T) {
	root := repoRoot(t)
	rulesPath := filepath.Join(root, ".claude", "rules", "rules.md")
	raw, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read rules.md at %s: %v", rulesPath, err)
	}
	content := string(raw)

	// --- Part 1: every cited repo-relative path must exist on disk. ---
	// The Search Scoping section lists ignore-globs to EXCLUDE from file
	// searches (e.g. `vendor/`, `tmp/`) — conventional patterns that need not
	// exist in any given checkout, not paths the document instructs a
	// contributor to run against. Drop that line before the existence scan so a
	// correct ignore-glob is never flagged as a dead path. (Drift in that
	// section's executor-prefix entries is still caught by the Part-2 negative
	// contract below, which is the designated mechanism for that correction.)
	scannable := dropSearchScopingLine(content)
	var missing []string
	seen := map[string]bool{}
	for _, candidate := range citedPaths(scannable) {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if _, statErr := os.Stat(filepath.Join(root, candidate)); statErr != nil {
			missing = append(missing, candidate)
		}
	}
	if len(missing) > 0 {
		t.Errorf("rules.md cites %d filesystem path(s) that do not exist on disk: %s",
			len(missing), strings.Join(missing, ", "))
	}

	// --- Part 2: the specific positive/negative accuracy contract. ---
	// Positive: the rebuild instruction must name the real mechanism.
	if !strings.Contains(content, "make core-images") {
		t.Errorf("rules.md must instruct the image rebuild via `make core-images`, but that token is absent")
	}

	// Negative: none of the currently-dead references may remain.
	deadRefs := []string{
		"deploy/build-images.sh",             // rules.md:20 — absent deploy/ path
		"deploy/docker-compose.yml",          // rules.md:20 — absent deploy/ path
		"`executors/claude-agent",            // rules.md:21,46 — bare prefix; real tree is lib/services/executors/...
		"docs/2026-04-25-stores-redesign.md", // rules.md:51 — absent docs/ path
	}
	for _, ref := range deadRefs {
		if strings.Contains(content, ref) {
			t.Errorf("rules.md still carries the dead reference %q; it must be replaced with the real path/mechanism", ref)
		}
	}
}

// dropSearchScopingLine removes the single "Exclude from file searches:" line
// from the rules content. Those backtick tokens are ignore-globs (search
// EXCLUSIONS), not run-against paths, so they are out of scope for the
// path-existence gate per the accuracy-check intent ("every filesystem path the
// document instructs contributors to run"). All other lines are preserved.
func dropSearchScopingLine(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Exclude from file searches:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// citedPaths extracts every backtick-quoted token from the markdown that looks
// like a literal, os.Stat-able repo-relative filesystem path. The contract
// (per the accuracy-gate design): a token qualifies when it contains a "/" AND
// either ends in "/" (a directory ref) or ends in a known file extension
// (.sh/.yml/.yaml/.md/.go). Illustrative, non-literal tokens are excluded:
// URLs (http://, https://), `make ...` targets, glob-bearing tokens ("*", e.g.
// lib/protocols/proto/v1/*.proto), and brace-expansion tokens ("{", e.g.
// .../{postgres,sqlite}/...). Each backtick span may contain a whole command
// (with spaces), so spans are split on whitespace into individual candidate
// tokens before the path test is applied.
func citedPaths(content string) []string {
	var out []string
	for _, span := range backtickSpans(content) {
		for _, tok := range strings.Fields(span) {
			if looksLikeRepoPath(tok) {
				out = append(out, tok)
			}
		}
	}
	return out
}

// looksLikeRepoPath applies the literal-path classifier described on citedPaths.
func looksLikeRepoPath(tok string) bool {
	if !strings.Contains(tok, "/") {
		return false
	}
	// Exclude illustrative / non-literal forms.
	if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
		return false
	}
	if strings.HasPrefix(tok, "make") {
		return false
	}
	if strings.ContainsAny(tok, "*{") {
		return false
	}
	// Directory ref, or a known concrete file extension.
	if strings.HasSuffix(tok, "/") {
		return true
	}
	for _, ext := range []string{".sh", ".yml", ".yaml", ".md", ".go"} {
		if strings.HasSuffix(tok, ext) {
			return true
		}
	}
	return false
}

// backtickSpans returns the content of every `...` backtick-delimited span in
// the markdown (the inner text, exclusive of the surrounding backticks).
func backtickSpans(content string) []string {
	var spans []string
	parts := strings.Split(content, "`")
	// Odd indices are inside a backtick pair; even indices are outside.
	for i := 1; i < len(parts); i += 2 {
		spans = append(spans, parts[i])
	}
	return spans
}

// repoRoot resolves the repository root from the test file's own directory,
// walking up until a go.work or .git marker is found, so the test is
// independent of the working directory `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	dir := filepath.Dir(thisFile)
	for {
		for _, marker := range []string{"go.work", ".git"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.work or .git (started at %s)", filepath.Dir(thisFile))
		}
		dir = parent
	}
}
