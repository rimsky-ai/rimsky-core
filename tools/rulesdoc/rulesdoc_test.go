// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package rulesdoc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// @story: rules-doc-accuracy
// @decision: doc-accuracy-gates
func TestRulesDoc_CitedPathsExist(t *testing.T) {
	root := repoRoot(t)
	rulesPath := filepath.Join(root, ".claude", "rules", "rules.md")
	raw, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read rules.md at %s: %v", rulesPath, err)
	}
	content := string(raw)

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

	if !strings.Contains(content, "make core-images") {
		t.Errorf("rules.md must instruct the image rebuild via `make core-images`, but that token is absent")
	}

	deadRefs := []string{
		"deploy/build-images.sh",
		"deploy/docker-compose.yml",
		"`executors/claude-agent",
		"docs/2026-04-25-stores-redesign.md",
	}
	for _, ref := range deadRefs {
		if strings.Contains(content, ref) {
			t.Errorf("rules.md still carries the dead reference %q; it must be replaced with the real path/mechanism", ref)
		}
	}
}

func TestLooksLikeRepoPath(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"lib/foundation/persistence/", true},
		{"lib/protocols/proto/v1/claim_producer.proto", true},
		{"lib/protocols/proto/v1/*.proto", false},
		{"deploy/values.json", true},
		{"deploy/values.toml", true},
		{".ok-planner/issues.jsonl", true},
		{"CLAUDE.md", true},
		{"./CLAUDE.md", true},
		{"RELEASING.md", true},
		{".golangci.yml", true},
		{"plumbline-cheatsheet.md", false},
		{"time.Sleep", false},
		{"make lint", false},
		{"https://github.com/example/repo", false},
	}
	for _, c := range cases {
		if got := looksLikeRepoPath(c.tok); got != c.want {
			t.Errorf("looksLikeRepoPath(%q) = %v, want %v", c.tok, got, c.want)
		}
	}
}

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

func looksLikeRepoPath(tok string) bool {
	tok = strings.TrimPrefix(tok, "./")
	if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
		return false
	}
	if strings.HasPrefix(tok, "make") {
		return false
	}
	if strings.ContainsAny(tok, "*{") {
		return false
	}
	if strings.HasSuffix(tok, "/") {
		return true
	}
	hasRepoExt := false
	for _, ext := range []string{".sh", ".yml", ".yaml", ".md", ".go", ".proto", ".ts", ".json", ".jsonl", ".toml"} {
		if strings.HasSuffix(tok, ext) {
			hasRepoExt = true
			break
		}
	}
	if !hasRepoExt {
		return false
	}
	if strings.Contains(tok, "/") {
		return true
	}
	return looksLikeRootLevelFilename(tok)
}

func looksLikeRootLevelFilename(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if strings.HasPrefix(base, ".") {
		return true
	}
	return base == strings.ToUpper(base)
}

func backtickSpans(content string) []string {
	var spans []string
	parts := strings.Split(content, "`")
	for i := 1; i < len(parts); i += 2 {
		spans = append(spans, parts[i])
	}
	return spans
}

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
