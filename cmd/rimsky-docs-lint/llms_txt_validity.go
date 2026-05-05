// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Markdown link target capturing group: matches [text](url).
var markdownLinkRE = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func runLLMSTxtValidity(args []string) error {
	fs := flag.NewFlagSet("llms-txt-validity", flag.ContinueOnError)
	llmsTxt := fs.String("llms-txt", "docs/agents/llms.txt", "path to llms.txt")
	llmsFull := fs.String("llms-full", "docs/agents/llms-full.txt", "path to llms-full.txt")
	repoRoot := fs.String("repo-root", ".", "repo root for link resolution")
	rootLLMSTxt := fs.String("root-llms-txt", "llms.txt", "repo-root copy")
	rootLLMSFull := fs.String("root-llms-full", "llms-full.txt", "repo-root copy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var hits []string
	// llms.txt is a curated index — link targets must resolve.
	hits = append(hits, validateLLMSTxtShape(*llmsTxt, *repoRoot, "docs/agents", true)...)
	// llms-full.txt is a generated corpus dump from concept bodies. Its
	// embedded links are concept-file "See also" relatives that don't
	// resolve from the concatenated file's location. Only validate
	// title + description on llms-full.
	hits = append(hits, validateLLMSTxtShape(*llmsFull, *repoRoot, "docs/agents", false)...)
	hits = append(hits, validateRootCopy(*llmsTxt, *rootLLMSTxt)...)
	hits = append(hits, validateRootCopy(*llmsFull, *rootLLMSFull)...)
	if len(hits) > 0 {
		return fmt.Errorf("llms-txt-validity failed:\n  - %s", strings.Join(hits, "\n  - "))
	}
	return nil
}

func validateLLMSTxtShape(path, repoRoot, baseDir string, validateLinks bool) []string {
	var hits []string
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "# ") {
		hits = append(hits, fmt.Sprintf("%s: must start with `# <Title>`", path))
	}
	sawDescription := false
	for i := 1; i < len(lines) && i < 6; i++ {
		if strings.HasPrefix(lines[i], "> ") {
			sawDescription = true
			break
		}
	}
	if !sawDescription {
		hits = append(hits, fmt.Sprintf("%s: must contain a `> <description>` blockquote near the top", path))
	}
	if !validateLinks {
		return hits
	}
	for i, line := range lines {
		for _, m := range markdownLinkRE.FindAllStringSubmatch(line, -1) {
			url := m[1]
			if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
				continue
			}
			// llms.txt URLs are conventionally docs-root-relative (e.g.
			// `concepts/four-layer-model.md`, `agents/examples/foo.md`).
			// Try the file's own directory first, then repoRoot, then
			// repoRoot + "docs/" — the third path covers the common
			// docs-root convention when the lint runs with the default
			// `-repo-root=.` from the repo root.
			base := filepath.Dir(path)
			tries := []string{
				filepath.Join(base, url),
				filepath.Join(repoRoot, url),
				filepath.Join(repoRoot, "docs", url),
			}
			resolved := false
			for _, candidate := range tries {
				if _, err := os.Stat(candidate); err == nil {
					resolved = true
					break
				}
			}
			if !resolved {
				hits = append(hits, fmt.Sprintf("%s:%d link target does not resolve: %s", path, i+1, url))
			}
		}
	}
	_ = baseDir
	return hits
}

func validateRootCopy(canonical, rootCopy string) []string {
	canon, err := os.ReadFile(canonical)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", canonical, err)}
	}
	got, err := os.ReadFile(rootCopy)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", rootCopy, err)}
	}
	if !bytes.Equal(canon, got) {
		return []string{fmt.Sprintf("%s does not byte-equal %s; run `make docs-roots` to update", rootCopy, canonical)}
	}
	return nil
}
