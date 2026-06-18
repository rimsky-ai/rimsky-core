// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

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

var skipDirs = map[string]struct{}{
	".git": {},
	".svn": {},
	".hg":  {},
}

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
			if cfg.classify(rel) == classExempt {
				return fs.SkipDir
			}
			return nil
		}
		kind, ok := sourceKindFor(d.Name())
		if !ok {
			return nil
		}
		c := cfg.classify(rel)
		if c == classExempt {
			return nil
		}
		if c == classUnknown {
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
