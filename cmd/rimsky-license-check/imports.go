// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// imports.go — verify the Apache → AGPL import-direction rule.
//
// Rule: every Apache-classified Go file's imports of `github.com/rimsky-ai/rimsky-core/...`
// must resolve to other Apache-classified packages. AGPL packages are
// unrestricted (they can import either layer freely).
//
// Implementation note: the import path
//   github.com/rimsky-ai/rimsky-core/foundation/locks
// resolves to a directory in this repo. We map module-paths back to repo
// paths by stripping the module-path prefix per the go.mod file we sit in.

package main

import (
	"go/parser"
	"go/token"
	"strings"
)

// modulePathPrefixes is the set of module paths defined in this repo that
// resolve to local directories. Each entry maps a module path to the
// repo-relative directory that contains its go.mod.
var modulePathPrefixes = []struct {
	module string // import-path prefix (with no trailing slash)
	dir    string // repo-relative dir of the module's go.mod (with no trailing slash; "" = repo root)
}{
	{"github.com/rimsky-ai/rimsky-core/foundation", "foundation"},
	{"github.com/rimsky-ai/rimsky-core/protocols", "protocols"},
	{"github.com/rimsky-ai/rimsky-core", ""}, // root module — must come last; longest-prefix-first sort below.
}

func init() {
	// Defense-in-depth: ensure longest module-path comes first so the
	// nested submodules (foundation/, protocols/) win the prefix match.
	for i := 0; i < len(modulePathPrefixes); i++ {
		for j := i + 1; j < len(modulePathPrefixes); j++ {
			if len(modulePathPrefixes[j].module) > len(modulePathPrefixes[i].module) {
				modulePathPrefixes[i], modulePathPrefixes[j] = modulePathPrefixes[j], modulePathPrefixes[i]
			}
		}
	}
}

// verifyImports parses each Apache-classified Go file and checks every
// rimsky import. AGPL files are not checked (they can import freely).
//
// Test files (`*_test.go`) are exempt from the import-direction rule:
// tests routinely need internal scaffolding (testcontainers fixtures,
// in-process database harnesses) and don't ship in published binaries
// or libraries. Apache-licensed test files may therefore import AGPL
// helpers such as `internal/pgtest/` without the lint complaining.
func verifyImports(files []fileEntry, cfg *licensingConfig) []violation {
	var out []violation
	fset := token.NewFileSet()
	for _, f := range files {
		if f.kind != kindGo {
			continue
		}
		if f.classification != classApache {
			continue
		}
		if strings.HasSuffix(f.relPath, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f.absPath, nil, parser.ImportsOnly)
		if err != nil {
			out = append(out, violation{path: f.relPath, message: "parse imports: " + err.Error()})
			continue
		}
		for _, imp := range af.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/rimsky-ai/rimsky-core") {
				continue
			}
			repoPath, ok := importToRepoPath(path)
			if !ok {
				continue
			}
			cls := cfg.classify(repoPath)
			if cls == classAGPL {
				out = append(out, violation{
					path:    f.relPath,
					message: "Apache file imports AGPL package: " + path,
				})
			}
		}
	}
	return out
}

// importToRepoPath maps an import path under github.com/rimsky-ai/rimsky-core/
// to the repo-relative directory that import resolves to.
func importToRepoPath(importPath string) (string, bool) {
	for _, mp := range modulePathPrefixes {
		if importPath == mp.module {
			return mp.dir, true
		}
		if strings.HasPrefix(importPath, mp.module+"/") {
			rest := strings.TrimPrefix(importPath, mp.module+"/")
			if mp.dir == "" {
				return rest, true
			}
			return mp.dir + "/" + rest, true
		}
	}
	return "", false
}
