// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: test-wallclock-lint-ratchet

package scan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

func packageStateViolations(sources map[string]string) []Violation {
	fset := token.NewFileSet()
	byPackage := map[string][]*ast.File{}
	relOf := map[*ast.File]string{}
	for rel, src := range sources {
		f, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			continue
		}
		byPackage[f.Name.Name] = append(byPackage[f.Name.Name], f)
		relOf[f] = rel
	}

	var out []Violation
	for _, files := range byPackage {
		state := packageLevelVars(files)
		if len(state) == 0 {
			continue
		}
		writers := packageStateWriters(files, state)
		for _, f := range files {
			rel := relOf[f]
			if !isTestCode(rel) {
				continue
			}
			lines := strings.Split(sources[rel], "\n")
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || !isTestFunction(fn) {
					continue
				}
				out = append(out, packageStateWritesIn(fset, rel, lines, fn, state, writers)...)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func packageLevelVars(files []*ast.File) map[string]bool {
	names := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if name.Name != "_" {
						names[name.Name] = true
					}
				}
			}
		}
	}
	return names
}

var testingParamTypes = map[string]bool{"T": true, "B": true, "F": true, "TB": true}

func isTestFunction(fn *ast.FuncDecl) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(fn.Name.Name, prefix) {
			return true
		}
	}
	if fn.Type.Params == nil {
		return false
	}
	for _, param := range fn.Type.Params.List {
		if namesATestingHandle(param.Type) {
			return true
		}
	}
	return false
}

func namesATestingHandle(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "testing" {
		return false
	}
	return testingParamTypes[sel.Sel.Name]
}

func packageStateWritesIn(fset *token.FileSet, rel string, lines []string, fn *ast.FuncDecl,
	state map[string]bool, writers map[string]map[string]bool) []Violation {
	locals := localNames(fn)
	var out []Violation
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			callee, ok := call.Fun.(*ast.Ident)
			if !ok || locals[callee.Name] || len(writers[callee.Name]) == 0 {
				return true
			}
			line := fset.Position(callee.Pos()).Line
			out = append(out, packageStateCallViolation(rel, lines, line,
				sortedNames(writers[callee.Name]), callee.Name, fn.Name.Name))
			return true
		}
		for _, target := range writeTargets(n) {
			ident := rootIdent(target)
			if ident == nil || locals[ident.Name] || !state[ident.Name] {
				continue
			}
			line := fset.Position(ident.Pos()).Line
			out = append(out, packageStateViolation(rel, lines, line, ident.Name, fn.Name.Name))
		}
		return true
	})
	return out
}

func writeTargets(n ast.Node) []ast.Expr {
	switch stmt := n.(type) {
	case *ast.AssignStmt:
		if stmt.Tok == token.DEFINE {
			return nil
		}
		return stmt.Lhs
	case *ast.IncDecStmt:
		return []ast.Expr{stmt.X}
	}
	return nil
}

func packageStateWriters(files []*ast.File, state map[string]bool) map[string]map[string]bool {
	bodies := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			bodies[fn.Name.Name] = fn
		}
	}
	writers := map[string]map[string]bool{}
	note := func(owner, written string) bool {
		if writers[owner] == nil {
			writers[owner] = map[string]bool{}
		}
		if writers[owner][written] {
			return false
		}
		writers[owner][written] = true
		return true
	}
	for grew := true; grew; {
		grew = false
		for name, fn := range bodies {
			params := parameterNames(fn)
			locals := localNames(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					callee, ok := call.Fun.(*ast.Ident)
					if !ok || locals[callee.Name] || (len(params) > 0 && !anyArgumentIsAParameter(call, params)) {
						return true
					}
					for _, written := range sortedNames(writers[callee.Name]) {
						if note(name, written) {
							grew = true
						}
					}
					return true
				}
				assign, ok := n.(*ast.AssignStmt)
				if !ok || assign.Tok == token.DEFINE || len(assign.Lhs) != len(assign.Rhs) {
					return true
				}
				for i, lhs := range assign.Lhs {
					ident := rootIdent(lhs)
					if ident == nil || locals[ident.Name] || !state[ident.Name] {
						continue
					}
					if !writesOnItsCallersBehalf(lhs, assign.Rhs[i], params) {
						continue
					}
					if note(name, ident.Name) {
						grew = true
					}
				}
				return true
			})
		}
	}
	return writers
}

func parameterNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn.Type.Params == nil {
		return out
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				out[name.Name] = true
			}
		}
	}
	return out
}

func anyArgumentIsAParameter(call *ast.CallExpr, params map[string]bool) bool {
	for _, arg := range call.Args {
		if isParameterValue(arg, params) {
			return true
		}
	}
	return false
}

func isParameterValue(expr ast.Expr, params map[string]bool) bool {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return params[e.Name]
		case *ast.ParenExpr:
			expr = e.X
		case *ast.SelectorExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.StarExpr:
			expr = e.X
		case *ast.TypeAssertExpr:
			expr = e.X
		case *ast.UnaryExpr:
			if e.Op != token.AND {
				return false
			}
			expr = e.X
		default:
			return false
		}
	}
}

func writesOnItsCallersBehalf(lhs, rhs ast.Expr, params map[string]bool) bool {
	if len(params) == 0 {
		return true
	}
	return isParameterValue(rhs, params) || indexesByAParameter(lhs, params)
}

func indexesByAParameter(lhs ast.Expr, params map[string]bool) bool {
	found := false
	ast.Inspect(lhs, func(n ast.Node) bool {
		idx, ok := n.(*ast.IndexExpr)
		if ok && isParameterValue(idx.Index, params) {
			found = true
		}
		return !found
	})
	return found
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func packageStateCallViolation(rel string, lines []string, line int, written []string, callee, inFunc string) Violation {
	source := ""
	if line-1 >= 0 && line-1 < len(lines) {
		source = strings.TrimSpace(lines[line-1])
	}
	named := strings.Join(written, ", ")
	own := parseMarker(source)
	if own.present && knownClass(own.class) && own.justification != "" {
		return Violation{File: rel, Line: line, Kind: KindInadmissible, Source: source,
			Detail: fmt.Sprintf("%s writes the package-level variable %s through its call to %s. No wait class admits "+
				"shared package state: the class markers claim elapsed time is not the verdict, and this write makes "+
				"execution order the verdict. Pass the value as an explicit parameter", inFunc, named, callee)}
	}
	return Violation{File: rel, Line: line, Kind: KindPackageState, Source: source,
		Detail: fmt.Sprintf("%s writes the package-level variable %s through its call to %s. Every test in the package "+
			"then reads what its siblings left behind, so execution order decides the verdict. Pass the value as an "+
			"explicit parameter", inFunc, named, callee)}
}

func packageStateViolation(rel string, lines []string, line int, name, inFunc string) Violation {
	source := ""
	if line-1 >= 0 && line-1 < len(lines) {
		source = strings.TrimSpace(lines[line-1])
	}
	own := parseMarker(source)
	if own.present && knownClass(own.class) && own.justification != "" {
		return Violation{File: rel, Line: line, Kind: KindInadmissible, Source: source,
			Detail: fmt.Sprintf("%s writes the package-level variable %q. No wait class admits shared package state: "+
				"the class markers claim elapsed time is not the verdict, and this write makes execution order the verdict. "+
				"Pass the value as an explicit parameter", inFunc, name)}
	}
	return Violation{File: rel, Line: line, Kind: KindPackageState, Source: source,
		Detail: fmt.Sprintf("%s writes the package-level variable %q. Every test in the package then reads what its siblings left behind, "+
			"so execution order decides the verdict. Pass the value as an explicit parameter", inFunc, name)}
}

func localNames(fn *ast.FuncDecl) map[string]bool {
	locals := map[string]bool{}
	declare := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				locals[name.Name] = true
			}
		}
	}
	declare(fn.Recv)
	declare(fn.Type.Params)
	declare(fn.Type.Results)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if stmt.Tok != token.DEFINE {
				return true
			}
			for _, lhs := range stmt.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					locals[ident.Name] = true
				}
			}
		case *ast.GenDecl:
			if stmt.Tok != token.VAR {
				return true
			}
			for _, spec := range stmt.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range value.Names {
						locals[name.Name] = true
					}
				}
			}
		case *ast.FuncLit:
			declare(stmt.Type.Params)
			declare(stmt.Type.Results)
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{stmt.Key, stmt.Value} {
				if ident, ok := e.(*ast.Ident); ok {
					locals[ident.Name] = true
				}
			}
		}
		return true
	})
	return locals
}

func rootIdent(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.IndexExpr:
			expr = e.X
		case *ast.SelectorExpr:
			expr = e.X
		case *ast.StarExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		default:
			return nil
		}
	}
}
