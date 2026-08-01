// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// @decision: config-yaml-loading-policy
package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

var lintSkipDirs = map[string]struct{}{
	".git":         {},
	".ok-planner":  {},
	"vendor":       {},
	"bin":          {},
	"tmp":          {},
	"gen":          {},
	"node_modules": {},
}

func walkGoSources(t *testing.T, root string, visit func(path string, file *ast.File, fset *token.FileSet)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := lintSkipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"gen"+string(filepath.Separator)) {
			return nil
		}
		af, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		visit(path, af, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

func inSharedLoaderPkg(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/lib/protocols/config/")
}

func TestNoOSExpandEnvOutsideSharedLoader(t *testing.T) {
	root := repoRoot(t)
	var findings []string
	walkGoSources(t, root, func(path string, file *ast.File, fset *token.FileSet) {
		if inSharedLoaderPkg(path) {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "os" && sel.Sel.Name == "ExpandEnv" {
				findings = append(findings, fset.Position(call.Pos()).String())
			}
			return true
		})
	})
	if len(findings) > 0 {
		t.Fatalf("os.ExpandEnv is forbidden outside %s (the shared loader). Route env expansion through the shared loader instead. Offenders:\n  %s",
			filepath.Join("lib", "protocols", "config"), strings.Join(findings, "\n  "))
	}
}

func TestNoYAMLUnmarshalOutsideSharedLoader(t *testing.T) {
	root := repoRoot(t)
	var findings []string
	walkGoSources(t, root, func(path string, file *ast.File, fset *token.FileSet) {
		if inSharedLoaderPkg(path) {
			return
		}
		if isTestFile(path) {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "yaml" && sel.Sel.Name == "Unmarshal" {
				findings = append(findings, fset.Position(call.Pos()).String())
			}
			return true
		})
	})
	if len(findings) > 0 {
		t.Fatalf("yaml.Unmarshal is forbidden outside %s (the shared loader). Route decoding through configload.DecodeStrict / LoadFile instead. Offenders:\n  %s",
			filepath.Join("lib", "protocols", "config"), strings.Join(findings, "\n  "))
	}
}

func TestNoDuplicateEnvExpanderHelpers(t *testing.T) {
	root := repoRoot(t)
	var findings []string
	walkGoSources(t, root, func(path string, file *ast.File, fset *token.FileSet) {
		if inSharedLoaderPkg(path) {
			return
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if expandsBracedOrCallsOsExpand(fn.Body) {
				findings = append(findings, fset.Position(fn.Pos()).String()+" "+fn.Name.Name)
			}
		}
	})
	if len(findings) > 0 {
		t.Fatalf("duplicate env-expansion helpers are forbidden outside %s. Delete and route through configload.ExpandEnv. Offenders:\n  %s",
			filepath.Join("lib", "protocols", "config"), strings.Join(findings, "\n  "))
	}
}

func expandsBracedOrCallsOsExpand(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name == "os" && (sel.Sel.Name == "Expand" || sel.Sel.Name == "ExpandEnv") {
			found = true
			return false
		}
		return true
	})
	return found
}
