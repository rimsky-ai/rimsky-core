// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: structured-log-kind-format

package scan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	KindMalformed = "malformed-log-kind"
	KindDynamic   = "unreadable-log-kind"
)

var KindPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*(\.[A-Z][A-Z0-9]*){2,}$`)

var scanRoots = []string{"cmd", "lib", "test", "tools"}

const ScannerOwnPackage = "tools/logkind-lint/scan/"

var messageArgIndex = map[string]int{
	"Debug":        0,
	"Info":         0,
	"Warn":         0,
	"Error":        0,
	"DebugContext": 1,
	"InfoContext":  1,
	"WarnContext":  1,
	"ErrorContext": 1,
	"Log":          2,
	"LogAttrs":     2,
}

type Violation struct {
	File   string
	Line   int
	Kind   string
	Detail string
	Source string
}

func (v Violation) Baselineable() bool { return v.Kind == KindMalformed }

type file struct {
	rel     string
	dir     string
	fset    *token.FileSet
	ast     *ast.File
	imports map[string]string
	lines   []string
}

func newFile(rel string, fset *token.FileSet, parsed *ast.File, src string) *file {
	return &file{rel: rel, dir: path.Dir(rel), fset: fset, ast: parsed,
		imports: importNames(parsed), lines: strings.Split(src, "\n")}
}

func ProcessLogViolations(repoRoot string) ([]Violation, error) {
	files, err := parseTree(repoRoot)
	if err != nil {
		return nil, err
	}
	return violationsInFiles(files), nil
}

func violationsInFiles(files []*file) []Violation {
	ix := newIndex(files)
	forwarders := forwardingKindParameters(ix, files)
	var out []Violation
	for _, f := range files {
		out = append(out, violationsInFile(ix, f)...)
		out = append(out, forwardedKindViolations(ix, f, forwarders)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func parseTree(repoRoot string) ([]*file, error) {
	var out []*file
	fset := token.NewFileSet()
	for _, root := range scanRoots {
		rootPath := filepath.Join(repoRoot, root)
		if _, err := os.Stat(rootPath); err != nil {
			continue
		}
		err := filepath.WalkDir(rootPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "gen" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") {
				return nil
			}
			rel, rerr := filepath.Rel(repoRoot, p)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, ScannerOwnPackage) {
				return nil
			}
			src, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			parsed, perr := parser.ParseFile(fset, p, src, 0)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", rel, perr)
			}
			out = append(out, newFile(rel, fset, parsed, string(src)))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func importNames(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = p
	}
	return out
}

func violationsInFile(ix *index, f *file) []Violation {
	var out []Violation
	ast.Inspect(f.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		msg, ok := ix.loggerKindArgument(f, call)
		if !ok {
			return true
		}
		if v, bad := ix.kindViolation(f, call, msg); bad {
			out = append(out, v)
		}
		return true
	})
	return out
}

func (ix *index) loggerKindArgument(f *file, call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	idx, ok := messageArgIndex[sel.Sel.Name]
	if !ok {
		return nil, false
	}
	if idx >= len(call.Args) {
		return nil, false
	}
	if !ix.isLoggerReceiver(f, sel.X) {
		return nil, false
	}
	return call.Args[idx], true
}

func (ix *index) kindViolation(f *file, call *ast.CallExpr, msg ast.Expr) (Violation, bool) {
	if _, ok := ix.parameterBehind(f, msg); ok {
		return Violation{}, false
	}
	line := f.fset.Position(msg.Pos()).Line
	lit, ok := msg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return Violation{File: f.rel, Line: line, Kind: KindDynamic,
			Detail: "the kind is not a raw string literal at the emit site, so no reader can enumerate it. Name the kind literally and put the varying part in a field.",
			Source: render(f, call)}, true
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil || KindPattern.MatchString(value) {
		return Violation{}, false
	}
	return Violation{File: f.rel, Line: line, Kind: KindMalformed,
		Detail: fmt.Sprintf("%q is not SUBSYSTEM.NOUN.VERB. A kind joins at least three segments with dots, and each segment starts with an upper-case letter and continues with upper-case letters and digits. Prose belongs in a field, never in the kind.", value),
		Source: render(f, call)}, true
}

func bindingsInFile(f *file) []binding {
	scopes := scopeExtents(f.ast)
	var out []binding
	add := func(id *ast.Ident, typeExpr, init, rangeOf ast.Expr, param *parameterRef) {
		if id == nil || id.Name == "_" {
			return
		}
		from, to := innermostScope(scopes, id.Pos())
		out = append(out, binding{name: id.Name, file: f, declPos: id.Pos(), scopeFrom: from, scopeTo: to,
			typeExpr: typeExpr, init: init, rangeOf: rangeOf, param: param})
	}
	dir := f.dir
	ast.Inspect(f.ast, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			addFuncNames(d.Type, d.Recv, ownerOf(d), dir, add)
		case *ast.FuncLit:
			addFuncNames(d.Type, nil, nil, dir, add)
		case *ast.AssignStmt:
			if d.Tok != token.DEFINE {
				return true
			}
			paired := len(d.Lhs) == len(d.Rhs)
			for i, lhs := range d.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				var init ast.Expr
				if paired {
					init = d.Rhs[i]
				}
				add(id, nil, init, nil, nil)
			}
		case *ast.ValueSpec:
			paired := d.Type == nil && len(d.Names) == len(d.Values)
			for i, id := range d.Names {
				var init ast.Expr
				if paired {
					init = d.Values[i]
				}
				add(id, d.Type, init, nil, nil)
			}
		case *ast.RangeStmt:
			if d.Tok != token.DEFINE {
				return true
			}
			if id, ok := d.Key.(*ast.Ident); ok {
				add(id, nil, nil, nil, nil)
			}
			if id, ok := d.Value.(*ast.Ident); ok {
				add(id, nil, nil, d.X, nil)
			}
		}
		return true
	})
	return out
}

func ownerOf(d *ast.FuncDecl) *ast.FuncDecl {
	if d.Recv != nil {
		return nil
	}
	return d
}

func addFuncNames(t *ast.FuncType, recv *ast.FieldList, owner *ast.FuncDecl, dir string,
	add func(*ast.Ident, ast.Expr, ast.Expr, ast.Expr, *parameterRef)) {
	if recv != nil {
		for _, field := range recv.List {
			for _, id := range field.Names {
				add(id, field.Type, nil, nil, nil)
			}
		}
	}
	if t == nil {
		return
	}
	if t.Params != nil {
		i := 0
		for _, field := range t.Params.List {
			for _, id := range field.Names {
				ref := parameterRef{owner: owner, dir: dir, index: i}
				add(id, field.Type, nil, nil, &ref)
				i++
			}
		}
	}
	if t.Results != nil {
		for _, field := range t.Results.List {
			for _, id := range field.Names {
				add(id, field.Type, nil, nil, nil)
			}
		}
	}
}

type extent struct {
	from token.Pos
	to   token.Pos
}

func scopeExtents(f *ast.File) []extent {
	out := []extent{{from: f.Pos(), to: f.End()}}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncDecl, *ast.FuncLit, *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
			*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
			out = append(out, extent{from: n.Pos(), to: n.End()})
		}
		return true
	})
	return out
}

func innermostScope(scopes []extent, at token.Pos) (token.Pos, token.Pos) {
	best := extent{}
	found := false
	for _, sc := range scopes {
		if at < sc.from || at >= sc.to {
			continue
		}
		if !found || sc.from > best.from || (sc.from == best.from && sc.to < best.to) {
			best, found = sc, true
		}
	}
	return best.from, best.to
}

func (ix *index) bindingFor(f *file, id *ast.Ident) (binding, bool) {
	var winner *binding
	for i := range ix.bindings[f.rel] {
		b := &ix.bindings[f.rel][i]
		if b.name != id.Name || b.declPos > id.Pos() || id.Pos() < b.scopeFrom || id.Pos() >= b.scopeTo {
			continue
		}
		if winner == nil || b.scopeFrom > winner.scopeFrom ||
			(b.scopeFrom == winner.scopeFrom && b.declPos > winner.declPos) {
			winner = b
		}
	}
	if winner == nil {
		return binding{}, false
	}
	return *winner, true
}

func (ix *index) parameterBehind(f *file, msg ast.Expr) (parameterRef, bool) {
	var id *ast.Ident
	switch e := msg.(type) {
	case *ast.Ident:
		id = e
	case *ast.SelectorExpr:
		root, ok := e.X.(*ast.Ident)
		if !ok {
			return parameterRef{}, false
		}
		id = root
	default:
		return parameterRef{}, false
	}
	b, ok := ix.bindingFor(f, id)
	if !ok || b.param == nil {
		return parameterRef{}, false
	}
	return *b.param, true
}

type forwardKey struct {
	dir  string
	name string
}

func forwardingKindParameters(ix *index, files []*file) map[forwardKey]map[int]bool {
	out := map[forwardKey]map[int]bool{}
	record := func(ref parameterRef) bool {
		if ref.owner == nil {
			return false
		}
		key := forwardKey{dir: ref.dir, name: ref.owner.Name.Name}
		if out[key] == nil {
			out[key] = map[int]bool{}
		}
		if out[key][ref.index] {
			return false
		}
		out[key][ref.index] = true
		return true
	}
	for grew := true; grew; {
		grew = false
		for _, f := range files {
			ast.Inspect(f.ast, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if msg, ok := ix.loggerKindArgument(f, call); ok {
					if ref, ok := ix.parameterBehind(f, msg); ok && record(ref) {
						grew = true
					}
					return true
				}
				for _, idx := range forwardedKindArguments(f, call, out) {
					if idx >= len(call.Args) {
						continue
					}
					if ref, ok := ix.parameterBehind(f, call.Args[idx]); ok && record(ref) {
						grew = true
					}
				}
				return true
			})
		}
	}
	return out
}

func forwardedKindViolations(ix *index, f *file, forwarders map[forwardKey]map[int]bool) []Violation {
	var out []Violation
	ast.Inspect(f.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, idx := range forwardedKindArguments(f, call, forwarders) {
			if idx >= len(call.Args) {
				continue
			}
			if v, bad := ix.kindViolation(f, call, call.Args[idx]); bad {
				out = append(out, v)
			}
		}
		return true
	})
	return out
}

func forwardedKindArguments(f *file, call *ast.CallExpr, forwarders map[forwardKey]map[int]bool) []int {
	var indices map[int]bool
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		indices = forwarders[forwardKey{dir: f.dir, name: fn.Name}]
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		if !ok {
			return nil
		}
		importPath, ok := f.imports[pkg.Name]
		if !ok {
			return nil
		}
		for key, args := range forwarders {
			if key.name == fn.Sel.Name && strings.HasSuffix(importPath, "/"+key.dir) {
				indices = args
				break
			}
		}
	default:
		return nil
	}
	var out []int
	for idx := range indices {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

func render(f *file, call *ast.CallExpr) string {
	line := f.fset.Position(call.Pos()).Line
	if line-1 >= len(f.lines) {
		return ""
	}
	return strings.TrimSpace(f.lines[line-1])
}

func CountsByFile(violations []Violation) map[string]int {
	counts := map[string]int{}
	for _, v := range violations {
		if !v.Baselineable() {
			continue
		}
		counts[v.File]++
	}
	return counts
}

func NonBaselineable(violations []Violation) []Violation {
	var out []Violation
	for _, v := range violations {
		if !v.Baselineable() {
			out = append(out, v)
		}
	}
	return out
}
