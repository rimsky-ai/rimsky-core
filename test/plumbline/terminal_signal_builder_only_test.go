// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
				strLit, rawText, ok := terminalTypeStringLit(kv.Value)
				if !ok {
					continue
				}
				value := strings.Trim(strLit.Value, `"`)
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

func terminalTypeStringLit(value ast.Expr) (*ast.BasicLit, string, bool) {
	if lit, ok := value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return lit, lit.Value, true
	}
	call, ok := value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, "", false
	}
	funcName := ""
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		funcName = fn.Name
	case *ast.SelectorExpr:
		funcName = fn.Sel.Name
	}
	if funcName != "TypePath" {
		return nil, "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil, "", false
	}
	return lit, fmt.Sprintf("TypePath(%s)", lit.Value), true
}
