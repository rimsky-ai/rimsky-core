// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type parityInterface struct {
	file     string
	name     string
	accessor string
}

var parityInterfaces = []parityInterface{
	{file: "node_runs.go", name: "Queue", accessor: "Queue"},
	{file: "claim_handles.go", name: "ClaimHandleTable", accessor: "ClaimHandles"},
	{file: "frames.go", name: "FrameTable", accessor: "Frames"},
}

const persistencePackageName = "persistence"

// @decision: parity-expansion
func TestDriverParitySuiteExercisesEveryRuntimeDependedMethod(t *testing.T) {
	repoRoot := findRepoRoot(t)
	persistenceRoot := filepath.Join(repoRoot, "lib", "foundation", "persistence")

	fset := token.NewFileSet()
	declared := declaredParityMethods(t, fset, persistenceRoot)
	accessorReturns := accessorInterfaceNames(t, fset, persistenceRoot)

	exercised := map[string]bool{}
	conformanceRoot := filepath.Join(persistenceRoot, "conformance")
	entries, err := os.ReadDir(conformanceRoot)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceRoot, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(conformanceRoot, e.Name()), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", e.Name(), perr)
		}
		collectParityCalls(file, accessorReturns, exercised)
	}

	var unexercised []string
	for method := range declared {
		if !exercised[method] {
			unexercised = append(unexercised, method)
		}
	}
	sort.Strings(unexercised)
	if len(unexercised) > 0 {
		t.Errorf("the driver-parity suite calls %d of the %d methods the parity interfaces declare. "+
			"These %d take no call on their own interface: %s. "+
			"Give every queue, claim-handle, and frame method the runtime depends on a case in "+
			"lib/foundation/persistence/conformance, which runs against both drivers.",
			len(declared)-len(unexercised), len(declared), len(unexercised), strings.Join(unexercised, ", "))
	}
}

func declaredParityMethods(t *testing.T, fset *token.FileSet, persistenceRoot string) map[string]bool {
	t.Helper()
	declared := map[string]bool{}
	for _, pi := range parityInterfaces {
		path := filepath.Join(persistenceRoot, pi.file)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		iface := findInterfaceType(file, pi.name)
		if iface == nil {
			t.Fatalf("%s declares no interface named %s", path, pi.name)
		}
		for _, m := range iface.Methods.List {
			for _, n := range m.Names {
				declared[pi.name+"."+n.Name] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no methods declared across %d parity interfaces", len(parityInterfaces))
	}
	return declared
}

func accessorInterfaceNames(t *testing.T, fset *token.FileSet, persistenceRoot string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(persistenceRoot)
	if err != nil {
		t.Fatalf("read %s: %v", persistenceRoot, err)
	}
	wanted := map[string]string{}
	for _, pi := range parityInterfaces {
		wanted[pi.accessor] = pi.name
	}
	found := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(persistenceRoot, e.Name()), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", e.Name(), perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			iface, ok := n.(*ast.InterfaceType)
			if !ok {
				return true
			}
			for _, m := range iface.Methods.List {
				fn, ok := m.Type.(*ast.FuncType)
				if !ok || fn.Params.NumFields() != 0 || fn.Results.NumFields() != 1 {
					continue
				}
				result, ok := fn.Results.List[0].Type.(*ast.Ident)
				if !ok {
					continue
				}
				for _, name := range m.Names {
					if wanted[name.Name] == result.Name {
						found[name.Name] = result.Name
					}
				}
			}
			return true
		})
	}
	for accessor, ifaceName := range wanted {
		if found[accessor] != ifaceName {
			t.Fatalf("declare an accessor method %s() %s in %s; the parity check resolves calls through it",
				accessor, ifaceName, persistenceRoot)
		}
	}
	return found
}

func findInterfaceType(file *ast.File, name string) *ast.InterfaceType {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			if iface, ok := ts.Type.(*ast.InterfaceType); ok {
				return iface
			}
		}
	}
	return nil
}

func collectParityCalls(file *ast.File, accessorReturns map[string]string, exercised map[string]bool) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bound := parityHandleBindings(fn, accessorReturns)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if iface := parityReceiverInterface(sel.X, accessorReturns, bound); iface != "" {
				exercised[iface+"."+sel.Sel.Name] = true
			}
			return true
		})
	}
}

func parityHandleBindings(fn *ast.FuncDecl, accessorReturns map[string]string) map[string]string {
	bound := map[string]string{}
	bindParams(fn.Type, bound)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			bindParams(node.Type, bound)
		case *ast.AssignStmt:
			if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			name, ok := node.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if iface := accessorCallInterface(node.Rhs[0], accessorReturns); iface != "" {
				bound[name.Name] = iface
			}
		case *ast.ValueSpec:
			if len(node.Names) != 1 || len(node.Values) != 1 {
				return true
			}
			if iface := accessorCallInterface(node.Values[0], accessorReturns); iface != "" {
				bound[node.Names[0].Name] = iface
			}
		}
		return true
	})
	return bound
}

func bindParams(fn *ast.FuncType, bound map[string]string) {
	if fn.Params == nil {
		return
	}
	for _, field := range fn.Params.List {
		sel, ok := field.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != persistencePackageName {
			continue
		}
		for _, pi := range parityInterfaces {
			if sel.Sel.Name != pi.name {
				continue
			}
			for _, name := range field.Names {
				bound[name.Name] = pi.name
			}
		}
	}
}

func parityReceiverInterface(recv ast.Expr, accessorReturns map[string]string, bound map[string]string) string {
	if iface := accessorCallInterface(recv, accessorReturns); iface != "" {
		return iface
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return bound[ident.Name]
	}
	return ""
}

func accessorCallInterface(expr ast.Expr, accessorReturns map[string]string) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return accessorReturns[sel.Sel.Name]
}
