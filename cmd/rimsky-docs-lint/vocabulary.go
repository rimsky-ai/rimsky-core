// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type vocabConfig struct {
	Forbidden []vocabRule `yaml:"forbidden"`
}

type vocabRule struct {
	Term        string   `yaml:"term"`
	Replacement string   `yaml:"replacement"`
	Scope       []string `yaml:"scope"`
}

var ignoreCommentRE = regexp.MustCompile(`<!--\s*vocabulary-lint-ignore:\s*([^\s>]+)\s*-->`)

func runVocabulary(args []string) error {
	fs := flag.NewFlagSet("vocabulary", flag.ContinueOnError)
	configPath := fs.String("config", "docs/.vocabulary-lint.yml", "path to vocabulary lint config")
	repoRoot := fs.String("repo-root", ".", "path to repository root for scope-glob resolution")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadVocabConfig(*configPath)
	if err != nil {
		return err
	}
	var hits []string
	for _, rule := range cfg.Forbidden {
		re, err := regexp.Compile(rule.Term)
		if err != nil {
			return fmt.Errorf("config: invalid regex %q: %w", rule.Term, err)
		}
		files, err := expandGlobs(*repoRoot, rule.Scope)
		if err != nil {
			return err
		}
		for _, path := range files {
			fileHits, err := scanFile(path, re, rule.Term, rule.Replacement)
			if err != nil {
				return err
			}
			hits = append(hits, fileHits...)
		}
	}
	if len(hits) > 0 {
		return fmt.Errorf("vocabulary lint found %d issue(s):\n  - %s\n(see docs/vocabulary.md)",
			len(hits), strings.Join(hits, "\n  - "))
	}
	return nil
}

func loadVocabConfig(path string) (*vocabConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &vocabConfig{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// expandGlobs resolves each pattern via filepath.Glob (joined with repoRoot).
// Single-level globs only — `**` is not supported. The .vocabulary-lint.yml
// config enumerates concrete directories instead, which keeps the matcher
// trivial and makes the lint's coverage auditable.
func expandGlobs(root string, patterns []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range patterns {
		if strings.Contains(p, "**") {
			return nil, fmt.Errorf("scope %q contains `**`; this lint expects single-level globs only", p)
		}
		full := filepath.Join(root, p)
		m, err := filepath.Glob(full)
		if err != nil {
			return nil, err
		}
		for _, match := range m {
			if _, ok := seen[match]; ok {
				continue
			}
			seen[match] = struct{}{}
			out = append(out, match)
		}
	}
	return out, nil
}

func scanFile(path string, re *regexp.Regexp, term, replacement string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var hits []string
	lineno := 0
	skipNext := false
	// inFrontmatter tracks whether we're inside a YAML frontmatter block on a
	// markdown file. The lint scanner treats the lines between the leading
	// `---` and the trailing `---` as off-limits for vocabulary checks: the
	// `deprecated_terms:` field IS the official place to declare deprecated
	// vocabulary, and the backticked-name rule (`Store`) needs to coexist
	// with `deprecated_terms: [Store, ...]`. Concept-file authors continue
	// to use HTML-comment ignores in the body where the deprecated word
	// genuinely appears in prose. Triggered only on `.md` files; non-markdown
	// scope entries (llms.txt, etc.) are unaffected.
	scanFrontmatter := strings.HasSuffix(path, ".md")
	inFrontmatter := false
	frontmatterEnded := false
	for scanner.Scan() {
		lineno++
		line := scanner.Text()

		// Track frontmatter boundary on markdown files.
		if scanFrontmatter && !frontmatterEnded {
			if lineno == 1 && line == "---" {
				inFrontmatter = true
				continue
			}
			if inFrontmatter && line == "---" {
				inFrontmatter = false
				frontmatterEnded = true
				continue
			}
			if inFrontmatter {
				// Skip vocabulary scanning inside frontmatter entirely.
				continue
			}
		}

		// If a previous line said "ignore the next line for term <X>", and
		// X matches this rule's term, skip the term-search on this line.
		if skipNext {
			skipNext = false
			continue
		}

		// Look for a vocabulary-lint-ignore comment on this line.
		if m := ignoreCommentRE.FindStringSubmatch(line); m != nil {
			ignoredTerm := m[1]
			// Decide whether the comment applies to this line (comment is
			// alongside the offending term) or to the next line (comment is
			// on its own line above the offender).
			stripped := ignoreCommentRE.ReplaceAllString(line, "")
			if termAppliesToLine(ignoredTerm, term) && re.FindStringIndex(stripped) != nil {
				// Same-line ignore: do not flag this line; do not carry over.
				continue
			}
			// Comment-only line preceding the offender: skip the next line.
			if termAppliesToLine(ignoredTerm, term) {
				skipNext = true
			}
			continue
		}

		if loc := re.FindStringIndex(line); loc != nil {
			hits = append(hits, fmt.Sprintf("%s:%d  %q → %s",
				path, lineno, line[loc[0]:loc[1]], replacement))
		}
	}
	return hits, scanner.Err()
}

// termAppliesToLine decides whether an ignore comment naming `ignoredTerm`
// suppresses a hit for `ruleTerm`. Match-by-substring: if the ignore comment
// names a term that appears in the rule's regex source, treat as applying.
// Conservative — false positives just leave a line scanned, which is the
// safe direction.
func termAppliesToLine(ignoredTerm, ruleTerm string) bool {
	return strings.Contains(ruleTerm, ignoredTerm) || strings.Contains(ignoredTerm, ruleTerm)
}
