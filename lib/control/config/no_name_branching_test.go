// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// @concept: service
func TestNoNameSpecificBranchingInServiceEntryLoops(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	var violations []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			rangeStmt, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			keyIdent, ok := rangeStmt.Key.(*ast.Ident)
			if !ok || keyIdent.Name != "name" {
				return true
			}
			ast.Inspect(rangeStmt.Body, func(inner ast.Node) bool {
				switch expr := inner.(type) {
				case *ast.BinaryExpr:
					if (expr.Op == token.EQL || expr.Op == token.NEQ) && comparesNameToStringLit(expr) {
						violations = append(violations, fmt.Sprintf("%s:%s", e.Name(), fset.Position(expr.Pos())))
					}
				case *ast.SwitchStmt:
					if tagIdent, ok := expr.Tag.(*ast.Ident); ok && tagIdent.Name == "name" {
						violations = append(violations, fmt.Sprintf("%s:%s", e.Name(), fset.Position(expr.Pos())))
					}
				}
				return true
			})
			return true
		})
	}
	if len(violations) > 0 {
		t.Fatalf("found production branching on a specific service entry name — "+
			"claim_producers/executors/publishers entries must stay opaque connection-level "+
			"map keys with no name-specific code path:\n%s", strings.Join(violations, "\n"))
	}
}

func comparesNameToStringLit(expr *ast.BinaryExpr) bool {
	isNameIdent := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == "name"
	}
	isStringLit := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Kind == token.STRING
	}
	return (isNameIdent(expr.X) && isStringLit(expr.Y)) || (isNameIdent(expr.Y) && isStringLit(expr.X))
}
