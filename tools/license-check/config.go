// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// config.go — parse licensing.yml and provide longest-prefix-match
// classification of repo paths into apache / agpl / exempt buckets.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type classification int

const (
	classUnknown classification = iota
	classApache
	classAGPL
	classExempt
)

func (c classification) String() string {
	switch c {
	case classApache:
		return "apache"
	case classAGPL:
		return "agpl"
	case classExempt:
		return "exempt"
	}
	return "unknown"
}

// licensingConfig is the parsed shape of licensing.yml. Each list is a set
// of path prefixes (or full paths) used for longest-prefix-match.
type licensingConfig struct {
	apachePrefixes []string
	agplPrefixes   []string
	exemptEntries  []string
}

func loadLicensingYAML(root string) (*licensingConfig, error) {
	path := filepath.Join(root, "licensing.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Apache []string `yaml:"apache"`
		AGPL   []string `yaml:"agpl"`
		Exempt []string `yaml:"exempt"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &licensingConfig{
		apachePrefixes: normalizePrefixes(doc.Apache),
		agplPrefixes:   normalizePrefixes(doc.AGPL),
		exemptEntries:  normalizePrefixes(doc.Exempt),
	}, nil
}

// normalizePrefixes strips trailing slashes for comparison (we always
// match against forward-slash repo-relative paths).
func normalizePrefixes(in []string) []string {
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = strings.TrimSuffix(p, "/")
	}
	// Sort longest-first so the longest match wins on lookup.
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// classify returns the classification of a repo-relative path. Longest
// prefix match wins. A path that matches no prefix returns classUnknown
// (which the walker treats as "default to apache" for top-level files
// per the design doc, and "skip" for unrecognized deep paths).
func (c *licensingConfig) classify(relPath string) classification {
	relPath = strings.TrimPrefix(relPath, "./")
	exempt, exemptLen := matchPrefix(c.exemptEntries, relPath)
	apache, apacheLen := matchPrefix(c.apachePrefixes, relPath)
	agpl, agplLen := matchPrefix(c.agplPrefixes, relPath)
	// Find the longest match across all three sets.
	winner, winLen := classUnknown, -1
	if exempt && exemptLen > winLen {
		winner, winLen = classExempt, exemptLen
	}
	if apache && apacheLen > winLen {
		winner, winLen = classApache, apacheLen
	}
	if agpl && agplLen > winLen {
		winner, winLen = classAGPL, agplLen //nolint:ineffassign,wastedassign,staticcheck // symmetric update; future classifications below would silently lose to AGPL otherwise.
	}
	return winner
}

// matchPrefix reports whether path is at-or-under any prefix in the list,
// returning the longest matched prefix's length.
func matchPrefix(prefixes []string, path string) (bool, int) {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true, len(p)
		}
	}
	return false, 0
}
