// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var citationCommentRE = regexp.MustCompile(`<!--\s*@source:\s*(concepts/[a-z0-9-]+\.md)\s*-->`)

type defOnlyFM struct {
	Definition string `yaml:"definition"`
}

func runCitationDrift(args []string) error {
	fs := flag.NewFlagSet("citation-drift", flag.ContinueOnError)
	publicSurface := fs.String("scope", "docs/concepts,docs/protocols,docs/humans", "comma-separated public-surface roots")
	conceptsDir := fs.String("concepts-dir", "docs/concepts", "path to concept files (citation targets)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	defs, err := loadConceptDefinitions(*conceptsDir)
	if err != nil {
		return err
	}
	var hits []string
	for _, root := range strings.Split(*publicSurface, ",") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			fileHits, err := scanCitations(path, defs)
			if err != nil {
				return err
			}
			hits = append(hits, fileHits...)
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(hits) > 0 {
		return fmt.Errorf("citation drift detected:\n  - %s", strings.Join(hits, "\n  - "))
	}
	return nil
}

func loadConceptDefinitions(dir string) (map[string]string, error) {
	defs := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return defs, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(raw, []byte("---\n")) {
			continue
		}
		end := bytes.Index(raw[4:], []byte("\n---\n"))
		if end < 0 {
			continue
		}
		fm := &defOnlyFM{}
		if err := yaml.Unmarshal(raw[4:4+end], fm); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		defs["concepts/"+e.Name()] = normalizeWhitespace(fm.Definition)
	}
	return defs, nil
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func scanCitations(path string, defs map[string]string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	var hits []string
	for i := 0; i < len(lines); i++ {
		m := citationCommentRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		targetKey := m[1]
		wantDef, ok := defs[targetKey]
		if !ok {
			hits = append(hits, fmt.Sprintf("%s:%d cites missing target %s", path, i+1, targetKey))
			continue
		}
		// Look for the next blockquote starting the next non-blank line.
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j >= len(lines) || !strings.HasPrefix(lines[j], "> ") {
			hits = append(hits, fmt.Sprintf("%s:%d citation must be immediately followed by a markdown blockquote starting with `> `", path, i+1))
			continue
		}
		// Collect blockquote lines until non-`>` line.
		var bq strings.Builder
		for ; j < len(lines) && strings.HasPrefix(lines[j], "> "); j++ {
			bq.WriteString(strings.TrimPrefix(lines[j], "> "))
			bq.WriteByte(' ')
		}
		gotDef := normalizeWhitespace(bq.String())
		if gotDef != wantDef {
			hits = append(hits, fmt.Sprintf("%s:%d citation drift; blockquote text does not match %s `definition` frontmatter\n      got:  %q\n      want: %q",
				path, i+1, targetKey, gotDef, wantDef))
		}
	}
	return hits, nil
}
