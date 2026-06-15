// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// walker.go — walk the repo tree, classify each source file, and return
// the set of files the verifier and stamper operate on.

package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// fileEntry is one source file the lint cares about.
type fileEntry struct {
	relPath        string
	absPath        string
	kind           sourceKind
	classification classification
}

type sourceKind int

const (
	kindGo sourceKind = iota + 1
	kindTS
	kindProto
	kindSQL
	kindShell
)

// skipDirs these directories outright when walking. Only VCS dirs are hardcoded;
// all other "skip me" directories (bin/, node_modules/, dist/, gen/, etc.)
// must be listed under `exempt:` in licensing.yml so the boundary map is the
// single source of truth.
var skipDirs = map[string]struct{}{
	".git": {},
	".svn": {},
	".hg":  {},
}

// sourceKindFor extensions we stamp/check.
func sourceKindFor(name string) (sourceKind, bool) {
	switch filepath.Ext(name) {
	case ".go":
		return kindGo, true
	case ".ts", ".tsx":
		return kindTS, true
	case ".proto":
		return kindProto, true
	case ".sql":
		return kindSQL, true
	case ".sh":
		return kindShell, true
	}
	return 0, false
}

func walkSourceFiles(root string, cfg *licensingConfig) ([]fileEntry, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []fileEntry
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(absRoot, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return fs.SkipDir
			}
			// @constraint: honor exempt-prefix that names a directory
			// (e.g. "bin/", "protocols/proto/v1/gen/").
			if cfg.classify(rel) == classExempt {
				return fs.SkipDir
			}
			return nil
		}
		kind, ok := sourceKindFor(d.Name())
		if !ok {
			return nil
		}
		// @deliberate: tests under proto/v1/gen/ etc. are excluded by the
		// dir skip; at this point a stray file matching exempt paths
		// still gets skipped here.
		c := cfg.classify(rel)
		if c == classExempt {
			return nil
		}
		if c == classUnknown {
			// @deliberate: a source file in an unclassified location is
			// itself a violation worth surfacing — record as classUnknown
			// so verify catches it via the import check rather than
			// silently mis-classifying. In practice licensing.yml covers
			// every directory the project ships; surfacing unclassified
			// source files as a hard error forces a config update.
			out = append(out, fileEntry{
				relPath:        rel,
				absPath:        path,
				kind:           kind,
				classification: classUnknown,
			})
			return nil
		}
		out = append(out, fileEntry{
			relPath:        rel,
			absPath:        path,
			kind:           kind,
			classification: c,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// @constraint: stable order for deterministic output.
	sortFiles(out)
	return out, nil
}

func sortFiles(in []fileEntry) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && strings.Compare(in[j-1].relPath, in[j].relPath) > 0; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}
