// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"go/parser"
	"go/token"
	"strings"
)

var modulePathPrefixes = []struct {
	module string
	dir    string
}{
	{"github.com/rimsky-ai/rimsky-core/lib/foundation", "foundation"},
	{"github.com/rimsky-ai/rimsky-core/lib/protocols", "protocols"},
	{"github.com/rimsky-ai/rimsky-core", ""},
}

func init() {
	for i := 0; i < len(modulePathPrefixes); i++ {
		for j := i + 1; j < len(modulePathPrefixes); j++ {
			if len(modulePathPrefixes[j].module) > len(modulePathPrefixes[i].module) {
				modulePathPrefixes[i], modulePathPrefixes[j] = modulePathPrefixes[j], modulePathPrefixes[i]
			}
		}
	}
}

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
			if isStdlibImport(path) {
				continue
			}
			if !strings.HasPrefix(path, "github.com/rimsky-ai/rimsky-core") {
				if v := verifyThirdPartyImport(f.relPath, path, cfg.thirdParty); v != nil {
					out = append(out, *v)
				}
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
