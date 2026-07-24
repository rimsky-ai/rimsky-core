// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
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

type licensingConfig struct {
	apachePrefixes []string
	agplPrefixes   []string
	exemptEntries  []string
}

func loadLicensingYAML(root string) (*licensingConfig, error) {
	path := filepath.Join(root, "licensing.yml")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Apache []string `yaml:"apache"`
		AGPL   []string `yaml:"agpl"`
		Exempt []string `yaml:"exempt"`
	}
	if err := configload.LoadFile(path, &doc); err != nil {
		return nil, err
	}
	return &licensingConfig{
		apachePrefixes: normalizePrefixes(doc.Apache),
		agplPrefixes:   normalizePrefixes(doc.AGPL),
		exemptEntries:  normalizePrefixes(doc.Exempt),
	}, nil
}

func normalizePrefixes(in []string) []string {
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = strings.TrimSuffix(p, "/")
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func (c *licensingConfig) classify(relPath string) classification {
	relPath = strings.TrimPrefix(relPath, "./")
	exempt, exemptLen := matchPrefix(c.exemptEntries, relPath)
	apache, apacheLen := matchPrefix(c.apachePrefixes, relPath)
	agpl, agplLen := matchPrefix(c.agplPrefixes, relPath)
	winner, winLen := classUnknown, -1
	if exempt && exemptLen > winLen {
		winner, winLen = classExempt, exemptLen
	}
	if apache && apacheLen > winLen {
		winner, winLen = classApache, apacheLen
	}
	if agpl && agplLen > winLen {
		winner = classAGPL
	}
	return winner
}

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
