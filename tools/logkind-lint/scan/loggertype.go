// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: structured-log-kind-format

package scan

import (
	"go/ast"
	"go/token"
	"strings"
)

const slogImportPath = "log/slog"

const maxResolutionDepth = 24

var slogLoggerFile = &file{rel: "log/slog/logger.go", dir: slogImportPath,
	imports: map[string]string{"slog": slogImportPath}}

var slogLoggerType = resolvedType{
	expr: &ast.SelectorExpr{X: ast.NewIdent("slog"), Sel: ast.NewIdent("Logger")},
	file: slogLoggerFile,
}

var slogLoggerConstructors = map[string]bool{"New": true, "Default": true, "With": true}

var slogLoggerDerivations = map[string]bool{"With": true, "WithGroup": true}

type resolvedType struct {
	expr ast.Expr
	file *file
}

type typeID struct {
	dir  string
	name string
}

type typeSite struct {
	file *file
	spec *ast.TypeSpec
}

type funcSite struct {
	file *file
	decl *ast.FuncDecl
}

type packageIndex struct {
	types   map[string]typeSite
	funcs   map[string]funcSite
	methods map[string]map[string]funcSite
	vars    map[string]binding
}

type parameterRef struct {
	owner *ast.FuncDecl
	dir   string
	index int
}

type binding struct {
	name      string
	file      *file
	declPos   token.Pos
	scopeFrom token.Pos
	scopeTo   token.Pos
	typeExpr  ast.Expr
	init      ast.Expr
	rangeOf   ast.Expr
	param     *parameterRef
}

type typeResult struct {
	rt resolvedType
	ok bool
}

type index struct {
	byDir    map[string]*packageIndex
	dirFor   map[string]string
	bindings map[string][]binding
	loggers  map[typeID]bool
	cache    map[ast.Expr]typeResult
	visiting map[ast.Expr]bool
}

func newIndex(files []*file) *index {
	ix := &index{
		byDir:    map[string]*packageIndex{},
		dirFor:   map[string]string{},
		bindings: map[string][]binding{},
		loggers:  map[typeID]bool{},
		cache:    map[ast.Expr]typeResult{},
		visiting: map[ast.Expr]bool{},
	}
	for _, f := range files {
		ix.collectPackageDecls(f)
		ix.bindings[f.rel] = bindingsInFile(f)
	}
	ix.mapImportDirs(files)
	ix.markLoggerTypes()
	return ix
}

func (ix *index) pkgFor(dir string) *packageIndex {
	pk, ok := ix.byDir[dir]
	if !ok {
		pk = &packageIndex{types: map[string]typeSite{}, funcs: map[string]funcSite{},
			methods: map[string]map[string]funcSite{}, vars: map[string]binding{}}
		ix.byDir[dir] = pk
	}
	return pk
}

func (ix *index) collectPackageDecls(f *file) {
	pk := ix.pkgFor(f.dir)
	for _, decl := range f.ast.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				pk.funcs[d.Name.Name] = funcSite{file: f, decl: d}
				continue
			}
			owner := receiverTypeName(d.Recv.List[0].Type)
			if owner == "" {
				continue
			}
			if pk.methods[owner] == nil {
				pk.methods[owner] = map[string]funcSite{}
			}
			pk.methods[owner][d.Name.Name] = funcSite{file: f, decl: d}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					pk.types[s.Name.Name] = typeSite{file: f, spec: s}
				case *ast.ValueSpec:
					if d.Tok != token.VAR && d.Tok != token.CONST {
						continue
					}
					for i, n := range s.Names {
						if n.Name == "_" {
							continue
						}
						b := binding{name: n.Name, file: f, typeExpr: s.Type}
						if s.Type == nil && len(s.Values) == len(s.Names) {
							b.init = s.Values[i]
						}
						pk.vars[n.Name] = b
					}
				}
			}
		}
	}
}

func receiverTypeName(e ast.Expr) string {
	switch x := underlyingExpr(e).(type) {
	case *ast.Ident:
		return x.Name
	}
	return ""
}

func underlyingExpr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		default:
			return e
		}
	}
}

func (ix *index) mapImportDirs(files []*file) {
	paths := map[string]bool{}
	for _, f := range files {
		for _, p := range f.imports {
			paths[p] = true
		}
	}
	for p := range paths {
		best := ""
		for dir := range ix.byDir {
			if dir == "." || dir == "" {
				continue
			}
			if p != dir && !strings.HasSuffix(p, "/"+dir) {
				continue
			}
			if len(dir) > len(best) {
				best = dir
			}
		}
		if best != "" {
			ix.dirFor[p] = best
		}
	}
}

func (ix *index) markLoggerTypes() {
	emits := map[typeID]bool{}
	specs := map[typeID]typeSite{}
	for dir, pk := range ix.byDir {
		for name, site := range pk.types {
			id := typeID{dir: dir, name: name}
			specs[id] = site
			for method, decl := range pk.methods[name] {
				if isLoggerShapedMethod(method, decl.decl.Type) {
					emits[id] = true
				}
			}
			if it, ok := site.spec.Type.(*ast.InterfaceType); ok && declaresALoggerShapedMethod(it) {
				emits[id] = true
			}
		}
	}
	for grew := true; grew; {
		grew = false
		for id, site := range specs {
			if emits[id] {
				continue
			}
			for _, emb := range embeddedTypeExprs(site.spec) {
				if !ix.embeddedTypeEmits(emb, site.file, emits) {
					continue
				}
				emits[id] = true
				grew = true
				break
			}
		}
	}
	ix.loggers = emits
}

func isLoggerShapedMethod(name string, ft *ast.FuncType) bool {
	idx, ok := messageArgIndex[name]
	if !ok || ft == nil || ft.Params == nil || len(ft.Params.List) == 0 {
		return false
	}
	var flat []ast.Expr
	for _, fld := range ft.Params.List {
		n := len(fld.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			flat = append(flat, fld.Type)
		}
	}
	if idx >= len(flat) {
		return false
	}
	msg, ok := flat[idx].(*ast.Ident)
	if !ok || msg.Name != "string" {
		return false
	}
	_, variadic := ft.Params.List[len(ft.Params.List)-1].Type.(*ast.Ellipsis)
	return variadic
}

func declaresALoggerShapedMethod(it *ast.InterfaceType) bool {
	if it == nil || it.Methods == nil {
		return false
	}
	for _, entry := range it.Methods.List {
		ft, ok := entry.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		for _, n := range entry.Names {
			if isLoggerShapedMethod(n.Name, ft) {
				return true
			}
		}
	}
	return false
}

func (ix *index) embeddedTypeEmits(e ast.Expr, f *file, emits map[typeID]bool) bool {
	rt := resolvedType{expr: e, file: f}
	if ix.isSlogLogger(rt) {
		return true
	}
	if it, ok := underlyingExpr(e).(*ast.InterfaceType); ok {
		return declaresALoggerShapedMethod(it)
	}
	id, ok := ix.namedTypeID(rt)
	return ok && emits[id]
}

func embeddedTypeExprs(spec *ast.TypeSpec) []ast.Expr {
	var out []ast.Expr
	switch t := spec.Type.(type) {
	case *ast.StructType:
		if t.Fields == nil {
			return nil
		}
		for _, fld := range t.Fields.List {
			if len(fld.Names) == 0 {
				out = append(out, fld.Type)
			}
		}
	case *ast.InterfaceType:
		if t.Methods == nil {
			return nil
		}
		for _, entry := range t.Methods.List {
			if len(entry.Names) == 0 {
				out = append(out, entry.Type)
			}
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.StarExpr:
		out = append(out, spec.Type)
	}
	return out
}

func (ix *index) isSlogLogger(rt resolvedType) bool {
	sel, ok := underlyingExpr(rt.expr).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Logger" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || rt.file == nil {
		return false
	}
	return rt.file.imports[pkg.Name] == slogImportPath
}

func (ix *index) namedTypeID(rt resolvedType) (typeID, bool) {
	if rt.file == nil {
		return typeID{}, false
	}
	switch x := underlyingExpr(rt.expr).(type) {
	case *ast.Ident:
		return typeID{dir: rt.file.dir, name: x.Name}, true
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return typeID{}, false
		}
		importPath, ok := rt.file.imports[pkg.Name]
		if !ok {
			return typeID{}, false
		}
		dir, ok := ix.dirFor[importPath]
		if !ok {
			return typeID{}, false
		}
		return typeID{dir: dir, name: x.Sel.Name}, true
	}
	return typeID{}, false
}

func (ix *index) namedTypeSite(rt resolvedType) (typeSite, typeID, bool) {
	id, ok := ix.namedTypeID(rt)
	if !ok {
		return typeSite{}, typeID{}, false
	}
	pk, ok := ix.byDir[id.dir]
	if !ok {
		return typeSite{}, id, false
	}
	site, ok := pk.types[id.name]
	return site, id, ok
}

func (ix *index) isLoggerType(rt resolvedType) bool {
	if ix.isSlogLogger(rt) {
		return true
	}
	if it, ok := underlyingExpr(rt.expr).(*ast.InterfaceType); ok {
		return declaresALoggerShapedMethod(it)
	}
	id, ok := ix.namedTypeID(rt)
	return ok && ix.loggers[id]
}

func (ix *index) isLoggerReceiver(f *file, e ast.Expr) bool {
	if id, ok := e.(*ast.Ident); ok && ix.namesTheSlogPackage(f, id) {
		return true
	}
	rt, ok := ix.typeOf(f, e, 0)
	return ok && ix.isLoggerType(rt)
}

func (ix *index) namesTheSlogPackage(f *file, id *ast.Ident) bool {
	if _, bound := ix.bindingFor(f, id); bound {
		return false
	}
	return f.imports[id.Name] == slogImportPath
}

func (ix *index) typeOf(f *file, e ast.Expr, depth int) (resolvedType, bool) {
	if e == nil || depth > maxResolutionDepth {
		return resolvedType{}, false
	}
	if cached, ok := ix.cache[e]; ok {
		return cached.rt, cached.ok
	}
	if ix.visiting[e] {
		return resolvedType{}, false
	}
	ix.visiting[e] = true
	rt, ok := ix.resolveType(f, e, depth)
	delete(ix.visiting, e)
	ix.cache[e] = typeResult{rt: rt, ok: ok}
	return rt, ok
}

func (ix *index) resolveType(f *file, e ast.Expr, depth int) (resolvedType, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return ix.typeOf(f, x.X, depth+1)
	case *ast.StarExpr:
		return ix.typeOf(f, x.X, depth+1)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return ix.typeOf(f, x.X, depth+1)
		}
		return resolvedType{}, false
	case *ast.Ident:
		return ix.typeOfIdent(f, x, depth)
	case *ast.SelectorExpr:
		return ix.typeOfSelector(f, x, depth)
	case *ast.CallExpr:
		return ix.typeOfCall(f, x, depth)
	case *ast.CompositeLit:
		if x.Type == nil {
			return resolvedType{}, false
		}
		return resolvedType{expr: x.Type, file: f}, true
	case *ast.TypeAssertExpr:
		if x.Type == nil {
			return resolvedType{}, false
		}
		return resolvedType{expr: x.Type, file: f}, true
	case *ast.IndexExpr:
		base, ok := ix.typeOf(f, x.X, depth+1)
		if !ok {
			return resolvedType{}, false
		}
		return elementType(base)
	}
	return resolvedType{}, false
}

func (ix *index) typeOfIdent(f *file, id *ast.Ident, depth int) (resolvedType, bool) {
	if b, ok := ix.bindingFor(f, id); ok {
		return ix.typeOfBinding(b, depth)
	}
	if _, isImport := f.imports[id.Name]; isImport {
		return resolvedType{}, false
	}
	if pk, ok := ix.byDir[f.dir]; ok {
		if b, ok := pk.vars[id.Name]; ok {
			return ix.typeOfBinding(b, depth)
		}
	}
	return resolvedType{}, false
}

func (ix *index) typeOfBinding(b binding, depth int) (resolvedType, bool) {
	if depth > maxResolutionDepth {
		return resolvedType{}, false
	}
	if b.typeExpr != nil {
		return resolvedType{expr: b.typeExpr, file: b.file}, true
	}
	if b.init != nil {
		return ix.typeOf(b.file, b.init, depth+1)
	}
	if b.rangeOf != nil {
		over, ok := ix.typeOf(b.file, b.rangeOf, depth+1)
		if !ok {
			return resolvedType{}, false
		}
		return elementType(over)
	}
	return resolvedType{}, false
}

func elementType(rt resolvedType) (resolvedType, bool) {
	switch t := rt.expr.(type) {
	case *ast.ArrayType:
		return resolvedType{expr: t.Elt, file: rt.file}, true
	case *ast.MapType:
		return resolvedType{expr: t.Value, file: rt.file}, true
	case *ast.ChanType:
		return resolvedType{expr: t.Value, file: rt.file}, true
	}
	return resolvedType{}, false
}

func (ix *index) typeOfSelector(f *file, sel *ast.SelectorExpr, depth int) (resolvedType, bool) {
	if pkg, ok := sel.X.(*ast.Ident); ok {
		if _, bound := ix.bindingFor(f, pkg); !bound {
			if importPath, isImport := f.imports[pkg.Name]; isImport {
				dir, known := ix.dirFor[importPath]
				if !known {
					return resolvedType{}, false
				}
				pk, ok := ix.byDir[dir]
				if !ok {
					return resolvedType{}, false
				}
				b, ok := pk.vars[sel.Sel.Name]
				if !ok {
					return resolvedType{}, false
				}
				return ix.typeOfBinding(b, depth+1)
			}
		}
	}
	recv, ok := ix.typeOf(f, sel.X, depth+1)
	if !ok {
		return resolvedType{}, false
	}
	return ix.fieldType(recv, sel.Sel.Name, depth+1)
}

func (ix *index) fieldType(recv resolvedType, name string, depth int) (resolvedType, bool) {
	if depth > maxResolutionDepth {
		return resolvedType{}, false
	}
	site, _, ok := ix.namedTypeSite(recv)
	if !ok {
		return resolvedType{}, false
	}
	st, ok := site.spec.Type.(*ast.StructType)
	if !ok || st.Fields == nil {
		return resolvedType{}, false
	}
	for _, fld := range st.Fields.List {
		for _, n := range fld.Names {
			if n.Name == name {
				return resolvedType{expr: fld.Type, file: site.file}, true
			}
		}
	}
	for _, fld := range st.Fields.List {
		if len(fld.Names) > 0 {
			continue
		}
		if rt, ok := ix.fieldType(resolvedType{expr: fld.Type, file: site.file}, name, depth+1); ok {
			return rt, true
		}
	}
	return resolvedType{}, false
}

func (ix *index) typeOfCall(f *file, call *ast.CallExpr, depth int) (resolvedType, bool) {
	switch fn := underlyingCallee(call.Fun).(type) {
	case *ast.Ident:
		pk := ix.pkgFor(f.dir)
		if site, ok := pk.funcs[fn.Name]; ok {
			return singleResultOf(site.decl.Type, site.file)
		}
		if _, ok := pk.types[fn.Name]; ok {
			return resolvedType{expr: fn, file: f}, true
		}
		if b, ok := ix.bindingFor(f, fn); ok {
			if ft, ok := b.typeExpr.(*ast.FuncType); ok {
				return singleResultOf(ft, b.file)
			}
		}
		return resolvedType{}, false
	case *ast.SelectorExpr:
		return ix.typeOfMethodCall(f, fn, depth)
	}
	return resolvedType{}, false
}

func underlyingCallee(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		default:
			return e
		}
	}
}

func (ix *index) typeOfMethodCall(f *file, fn *ast.SelectorExpr, depth int) (resolvedType, bool) {
	if pkg, ok := fn.X.(*ast.Ident); ok {
		if _, bound := ix.bindingFor(f, pkg); !bound {
			if importPath, isImport := f.imports[pkg.Name]; isImport {
				if importPath == slogImportPath {
					if slogLoggerConstructors[fn.Sel.Name] {
						return slogLoggerType, true
					}
					return resolvedType{}, false
				}
				dir, known := ix.dirFor[importPath]
				if !known {
					return resolvedType{}, false
				}
				pk, ok := ix.byDir[dir]
				if !ok {
					return resolvedType{}, false
				}
				if site, ok := pk.funcs[fn.Sel.Name]; ok {
					return singleResultOf(site.decl.Type, site.file)
				}
				if _, ok := pk.types[fn.Sel.Name]; ok {
					return resolvedType{expr: fn, file: f}, true
				}
				return resolvedType{}, false
			}
		}
	}
	recv, ok := ix.typeOf(f, fn.X, depth+1)
	if !ok {
		return resolvedType{}, false
	}
	if ix.isSlogLogger(recv) && slogLoggerDerivations[fn.Sel.Name] {
		return slogLoggerType, true
	}
	return ix.methodResult(recv, fn.Sel.Name, depth+1)
}

func (ix *index) methodResult(recv resolvedType, name string, depth int) (resolvedType, bool) {
	if depth > maxResolutionDepth {
		return resolvedType{}, false
	}
	site, id, ok := ix.namedTypeSite(recv)
	if !ok {
		return resolvedType{}, false
	}
	if m, ok := ix.byDir[id.dir].methods[id.name][name]; ok {
		return singleResultOf(m.decl.Type, m.file)
	}
	switch t := site.spec.Type.(type) {
	case *ast.InterfaceType:
		if t.Methods == nil {
			return resolvedType{}, false
		}
		for _, entry := range t.Methods.List {
			for _, n := range entry.Names {
				if n.Name != name {
					continue
				}
				if ft, ok := entry.Type.(*ast.FuncType); ok {
					return singleResultOf(ft, site.file)
				}
			}
		}
		for _, entry := range t.Methods.List {
			if len(entry.Names) > 0 {
				continue
			}
			if rt, ok := ix.methodResult(resolvedType{expr: entry.Type, file: site.file}, name, depth+1); ok {
				return rt, true
			}
		}
	case *ast.StructType:
		if t.Fields == nil {
			return resolvedType{}, false
		}
		for _, fld := range t.Fields.List {
			if len(fld.Names) > 0 {
				continue
			}
			embedded := resolvedType{expr: fld.Type, file: site.file}
			if ix.isSlogLogger(embedded) && slogLoggerDerivations[name] {
				return slogLoggerType, true
			}
			if rt, ok := ix.methodResult(embedded, name, depth+1); ok {
				return rt, true
			}
		}
	}
	return resolvedType{}, false
}

func singleResultOf(ft *ast.FuncType, f *file) (resolvedType, bool) {
	if ft == nil || ft.Results == nil {
		return resolvedType{}, false
	}
	count := 0
	var only ast.Expr
	for _, fld := range ft.Results.List {
		n := len(fld.Names)
		if n == 0 {
			n = 1
		}
		count += n
		only = fld.Type
	}
	if count != 1 {
		return resolvedType{}, false
	}
	return resolvedType{expr: only, file: f}, true
}
