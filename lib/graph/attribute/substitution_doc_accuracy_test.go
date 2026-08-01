// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attribute

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

const resolverDispatchFunc = "resolveDirectiveValueRaw"

var docKindPattern = regexp.MustCompile(`\{\{(\w+)\.`)

// @story: substitution-doc-accuracy
func TestSubstitutionDocMatchesResolverDispatchSet(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "substitution.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse substitution.go: %v", err)
	}

	documented := documentedSourceKinds(t, file)
	dispatched := dispatchedSourceKinds(t, file)

	for kind := range dispatched {
		if !documented[kind] {
			t.Errorf("resolver dispatch arm %q is not listed in the package doc's source-kind list; documented=%v dispatched=%v",
				kind, sortedKeys(documented), sortedKeys(dispatched))
		}
	}
	for kind := range documented {
		if !dispatched[kind] {
			t.Errorf("package doc lists source kind %q but the resolver's dispatch set (func %s) has no case for it; documented=%v dispatched=%v",
				kind, resolverDispatchFunc, sortedKeys(documented), sortedKeys(dispatched))
		}
	}
}

func documentedSourceKinds(t *testing.T, file *ast.File) map[string]bool {
	t.Helper()
	if file.Doc == nil {
		t.Fatalf("substitution.go has no package doc comment")
	}
	matches := docKindPattern.FindAllStringSubmatch(file.Doc.Text(), -1)
	if len(matches) == 0 {
		t.Fatalf("package doc has no {{<kind>.…}} bullets to extract")
	}
	out := map[string]bool{}
	for _, m := range matches {
		out[m[1]] = true
	}
	return out
}

func dispatchedSourceKinds(t *testing.T, file *ast.File) map[string]bool {
	t.Helper()
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name.Name == resolverDispatchFunc {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatalf("func %s not found in substitution.go", resolverDispatchFunc)
	}

	var sw *ast.SwitchStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if s, ok := n.(*ast.SwitchStmt); ok && sw == nil {
			sw = s
			return false
		}
		return true
	})
	if sw == nil {
		t.Fatalf("func %s has no switch statement to enumerate dispatch arms from", resolverDispatchFunc)
	}

	out := map[string]bool{}
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok || cc.List == nil {
			continue
		}
		for _, expr := range cc.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("dispatch switch case is not a string literal: %#v", expr)
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote case literal %q: %v", lit.Value, err)
			}
			out[val] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("no case arms found in %s's dispatch switch", resolverDispatchFunc)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
