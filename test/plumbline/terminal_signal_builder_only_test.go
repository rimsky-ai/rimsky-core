// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTerminalSignalConstruction_RoutesOnlyThroughBuilders(t *testing.T) {
	repoRoot := findRepoRoot(t)

	allowedFiles := map[string]bool{
		filepath.Join(repoRoot, "lib", "foundation", "signal", "payloads.go"): true,
	}

	skipDirs := map[string]bool{
		".git":         true,
		".ok-planner":  true,
		"node_modules": true,
		"bin":          true,
		"tmp":          true,
	}

	fset := token.NewFileSet()
	constants := collectStringConstants(t, repoRoot, skipDirs, fset)
	var violations []string

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if allowedFiles[path] {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Signal" {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || (pkgIdent.Name != "signal" && pkgIdent.Name != "signalpkg") {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Type" {
					continue
				}
				value, rawText, ok := terminalTypePathValue(kv.Value, constants)
				if !ok {
					continue
				}
				if strings.HasPrefix(value, "terminal/") {
					relPath, _ := filepath.Rel(repoRoot, path)
					violations = append(violations, fmt.Sprintf("%s:%s: signal.Signal{Type: %s, ...} "+
						"constructs a terminal signal inline instead of routing through "+
						"BuildTerminalSuccessSignal/BuildTerminalErrorSignal",
						relPath, fset.Position(kv.Pos()), rawText))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoRoot, err)
	}
	if len(violations) > 0 {
		t.Fatalf("found terminal signal construction bypassing the single typed builders "+
			"(lib/foundation/signal/payloads.go::BuildTerminalSuccessSignal / BuildTerminalErrorSignal):\n%s",
			strings.Join(violations, "\n"))
	}
}

func collectStringConstants(t *testing.T, repoRoot string, skipDirs map[string]bool, fset *token.FileSet) map[string]string {
	t.Helper()
	type pending struct {
		name string
		expr ast.Expr
	}
	var specs []pending
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, n := range vs.Names {
					specs = append(specs, pending{name: n.Name, expr: vs.Values[i]})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("collect constants under %s: %v", repoRoot, err)
	}

	out := map[string]string{}
	for {
		progressed := false
		for _, sp := range specs {
			if _, done := out[sp.name]; done {
				continue
			}
			if v, ok := evalStringConstExpr(sp.expr, out); ok {
				out[sp.name] = v
				progressed = true
			}
		}
		if !progressed {
			return out
		}
	}
}

func evalStringConstExpr(expr ast.Expr, constants map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	case *ast.Ident:
		v, ok := constants[e.Name]
		return v, ok
	case *ast.SelectorExpr:
		v, ok := constants[e.Sel.Name]
		return v, ok
	case *ast.ParenExpr:
		return evalStringConstExpr(e.X, constants)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, lok := evalStringConstExpr(e.X, constants)
		r, rok := evalStringConstExpr(e.Y, constants)
		if !lok || !rok {
			return "", false
		}
		return l + r, true
	}
	return "", false
}

func terminalTypePathValue(value ast.Expr, constants map[string]string) (string, string, bool) {
	if call, ok := value.(*ast.CallExpr); ok && len(call.Args) == 1 {
		funcName := ""
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			funcName = fn.Name
		case *ast.SelectorExpr:
			funcName = fn.Sel.Name
		}
		if funcName != "TypePath" {
			return "", "", false
		}
		resolved, ok := evalStringConstExpr(call.Args[0], constants)
		if !ok {
			return "", "", false
		}
		return resolved, fmt.Sprintf("TypePath(%s)", exprText(call.Args[0])), true
	}
	resolved, ok := evalStringConstExpr(value, constants)
	if !ok {
		return "", "", false
	}
	return resolved, exprText(value), true
}

func exprText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Value
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.BinaryExpr:
		return exprText(e.X) + " + " + exprText(e.Y)
	case *ast.ParenExpr:
		return "(" + exprText(e.X) + ")"
	}
	return "<expr>"
}
