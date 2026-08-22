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
	"strconv"
	"strings"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: event-log
// @decision: event-log-kind-enum
func TestEveryOperationalEventKindHasAnEmitSite(t *testing.T) {
	repoRoot := findRepoRoot(t)
	emitted := emitSitesByConstructor(t, repoRoot)

	var declared []string
	for value, name := range genv1.OperationalKind_name {
		if value == int32(genv1.OperationalKind_OPERATIONAL_KIND_UNSPECIFIED) {
			continue
		}
		declared = append(declared, name)
	}
	sort.Strings(declared)

	var writerless []string
	for _, name := range declared {
		ctor := constructorForEnumName(name)
		if len(emitted[ctor]) == 0 {
			writerless = append(writerless, name+" (expected an "+ctor+"() value reaching an Events().Append)")
		}
	}
	if len(writerless) > 0 {
		t.Fatalf("%d of %d declared operational event kinds have no emit site; a filterable kind nothing writes "+
			"returns an empty feed a reader cannot tell apart from \"this never happened here\". The constructor's "+
			"value must reach an Events().Append call, directly or through one named value, one returning "+
			"function, or one wrapper's parameter; a mention in a switch arm or a filter is not an emit site:\n%s",
			len(writerless), len(declared), strings.Join(writerless, "\n"))
	}
	t.Logf("checked all %d operational kinds declared by the OperationalKind enum; each reaches at least one Events().Append", len(declared))
}

type kindGraph struct {
	fset *token.FileSet

	packageValues map[string]map[string]bool

	functionReturns map[string]map[string]bool

	callArgs map[string]map[int][]kindExpr

	appends []kindExpr
}

type kindExpr struct {
	expr    ast.Expr
	fn      *ast.FuncDecl
	site    string
	visited map[string]bool
}

func emitSitesByConstructor(t *testing.T, repoRoot string) map[string][]string {
	t.Helper()
	g := newKindGraph()
	parsed := make([]*ast.File, 0)
	for _, path := range productGoFiles(t, repoRoot) {
		f, err := parser.ParseFile(g.fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		parsed = append(parsed, f)
	}
	return g.emitSites(repoRoot, parsed)
}

func (g *kindGraph) emitSites(repoRoot string, parsed []*ast.File) map[string][]string {
	for _, f := range parsed {
		g.collectPackageValues(f)
	}
	for _, f := range parsed {
		g.collectFunctionShapes(repoRoot, f)
	}
	out := map[string][]string{}
	for _, site := range g.appends {
		for ctor := range g.resolve(site) {
			out[ctor] = append(out[ctor], site.site)
		}
	}
	return out
}

func newKindGraph() *kindGraph {
	return &kindGraph{
		fset:            token.NewFileSet(),
		packageValues:   map[string]map[string]bool{},
		functionReturns: map[string]map[string]bool{},
		callArgs:        map[string]map[int][]kindExpr{},
	}
}

// @decision: event-log-kind-enum
func TestEmitSiteScanCountsOnlyKindsThatReachAnAppend(t *testing.T) {
	sources := map[string]string{
		"filter.go": `package p
func filterOnly(k string) bool {
	switch k {
	case events.KindNeverWritten().String():
		return true
	}
	return events.KindAlsoNeverWritten() == events.KindAlsoNeverWritten()
}`,
		"emit.go": `package p
var auditKind = events.KindThroughAPackageValue()
func direct(ctx C, tx T) error {
	return p.Events().Append(ctx, EventAppendInput{Kind: events.KindWrittenDirectly()}, tx)
}
func wrapper(ctx C, kind K, tx T) error {
	return p.Events().Append(ctx, EventAppendInput{Kind: kind}, tx)
}
func caller(ctx C, tx T) error {
	return wrapper(ctx, auditKind, tx)
}`,
	}
	g := newKindGraph()
	parsed := make([]*ast.File, 0, len(sources))
	for name, src := range sources {
		f, err := parser.ParseFile(g.fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed = append(parsed, f)
	}
	sites := g.emitSites(".", parsed)

	for _, ctor := range []string{"KindWrittenDirectly", "KindThroughAPackageValue"} {
		if len(sites[ctor]) == 0 {
			t.Errorf("%s reaches an Events().Append and must count as an emit site", ctor)
		}
	}
	for _, ctor := range []string{"KindNeverWritten", "KindAlsoNeverWritten"} {
		if len(sites[ctor]) > 0 {
			t.Errorf("%s only appears in a switch arm and a comparison; it must not count as an emit site, got %v",
				ctor, sites[ctor])
		}
	}
}

func productGoFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	skipDirs := map[string]bool{
		".git": true, ".ok-planner": true, "node_modules": true, "bin": true, "tmp": true,
		"gen": true, "conformance": true, "events": true,
	}
	var out []string
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
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoRoot, err)
	}
	return out
}

func kindConstructorName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || !strings.HasSuffix(pkg.Name, "events") && pkg.Name != "events" {
		return ""
	}
	if !strings.HasPrefix(sel.Sel.Name, "Kind") {
		return ""
	}
	return sel.Sel.Name
}

func (g *kindGraph) collectPackageValues(f *ast.File) {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || (gen.Tok != token.VAR && gen.Tok != token.CONST) {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if ctor := kindConstructorName(vs.Values[i]); ctor != "" {
					if g.packageValues[name.Name] == nil {
						g.packageValues[name.Name] = map[string]bool{}
					}
					g.packageValues[name.Name][ctor] = true
				}
			}
		}
	}
}

func (g *kindGraph) collectFunctionShapes(repoRoot string, f *ast.File) {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.ReturnStmt:
				for _, res := range node.Results {
					ctors := g.literalOrPackageValue(res)
					for ctor := range ctors {
						if g.functionReturns[fn.Name.Name] == nil {
							g.functionReturns[fn.Name.Name] = map[string]bool{}
						}
						g.functionReturns[fn.Name.Name][ctor] = true
					}
				}
			case *ast.CallExpr:
				g.recordCallArgs(node, fn)
				if kindArg, ok := appendKindArgument(node); ok {
					rel, _ := filepath.Rel(repoRoot, g.fset.Position(node.Pos()).Filename)
					g.appends = append(g.appends, kindExpr{
						expr: kindArg,
						fn:   fn,
						site: rel + ":" + strconv.Itoa(g.fset.Position(node.Pos()).Line),
					})
				}
			}
			return true
		})
	}
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

func (g *kindGraph) recordCallArgs(call *ast.CallExpr, fn *ast.FuncDecl) {
	name := calleeName(call)
	if name == "" {
		return
	}
	for i, arg := range call.Args {
		if g.callArgs[name] == nil {
			g.callArgs[name] = map[int][]kindExpr{}
		}
		g.callArgs[name][i] = append(g.callArgs[name][i], kindExpr{expr: arg, fn: fn})
	}
}

func appendKindArgument(call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Append" {
		return nil, false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	if calleeName(inner) != "Events" {
		return nil, false
	}
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Kind" {
				continue
			}
			return kv.Value, true
		}
	}
	return nil, false
}

func (g *kindGraph) literalOrPackageValue(expr ast.Expr) map[string]bool {
	if ctor := kindConstructorName(expr); ctor != "" {
		return map[string]bool{ctor: true}
	}
	if name := symbolName(expr); name != "" {
		return g.packageValues[name]
	}
	return nil
}

func symbolName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

func (g *kindGraph) resolve(site kindExpr) map[string]bool {
	if site.visited == nil {
		site.visited = map[string]bool{}
	}
	out := map[string]bool{}
	for ctor := range g.literalOrPackageValue(site.expr) {
		out[ctor] = true
	}
	if call, ok := site.expr.(*ast.CallExpr); ok {
		for ctor := range g.functionReturns[calleeName(call)] {
			out[ctor] = true
		}
	}
	ident, ok := site.expr.(*ast.Ident)
	if !ok || site.fn == nil {
		return out
	}
	for ctor := range g.localAssignments(site.fn, ident.Name) {
		out[ctor] = true
	}
	if idx, isParam := parameterIndex(site.fn, ident.Name); isParam {
		key := site.fn.Name.Name + "#" + strconv.Itoa(idx)
		if !site.visited[key] {
			site.visited[key] = true
			for _, arg := range g.callArgs[site.fn.Name.Name][idx] {
				arg.visited = site.visited
				for ctor := range g.resolve(arg) {
					out[ctor] = true
				}
			}
		}
	}
	return out
}

func (g *kindGraph) localAssignments(fn *ast.FuncDecl, name string) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			if symbolName(lhs) != name {
				continue
			}
			rhs := assign.Rhs[0]
			if len(assign.Rhs) == len(assign.Lhs) {
				rhs = assign.Rhs[i]
			}
			for ctor := range g.literalOrPackageValue(rhs) {
				out[ctor] = true
			}
			if call, ok := rhs.(*ast.CallExpr); ok {
				for ctor := range g.functionReturns[calleeName(call)] {
					out[ctor] = true
				}
			}
		}
		return true
	})
	return out
}

func parameterIndex(fn *ast.FuncDecl, name string) (int, bool) {
	if fn.Type.Params == nil {
		return 0, false
	}
	idx := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			idx++
			continue
		}
		for _, ident := range field.Names {
			if ident.Name == name {
				return idx, true
			}
			idx++
		}
	}
	return 0, false
}

func constructorForEnumName(enumName string) string {
	trimmed := strings.TrimPrefix(enumName, "OPERATIONAL_KIND_")
	var b strings.Builder
	b.WriteString("Kind")
	for _, part := range strings.Split(trimmed, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(strings.ToLower(part[1:]))
	}
	return b.String()
}
