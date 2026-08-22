// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: expected-attributes-schema-closed
// @concept: observability
// @concept: executor

package plumbline

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
	httpnode "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
	verifiershapechecks "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks"
)

const stubModePackageDir = "../../../protocols/conformance/stubmode"

var dispatchBagNames = map[string]bool{
	"ud":         true,
	"attrs":      true,
	"attributes": true,
	"Attributes": true,
}

type bundledExecutor struct {
	name   string
	dir    string
	schema []byte
}

func bundledExecutors() []bundledExecutor {
	return []bundledExecutor{
		{"verifier-http", "../../executors/verifier-http", verifierhttp.SchemaBytes()},
		{"verifier-shape-checks", "../../executors/verifier-shape-checks", verifiershapechecks.SchemaBytes()},
		{"http-node", "../../executors/http-node", httpnode.SchemaBytes()},
		{"claude-agent", "../../executors/claude-agent", claudeagent.SchemaBytes()},
	}
}

func TestBundledExecutorSchemasDeclareEveryAttributeTheyRead(t *testing.T) {
	stubConstants, stubFuncKeys := readStubModeSignature(t)
	stubSignature := map[string]bool{}
	for _, v := range stubConstants {
		stubSignature[v] = true
	}

	executors := bundledExecutors()
	for _, e := range executors {
		read, unbounded := attributeKeysRead(t, e.dir, stubConstants, stubFuncKeys)
		for _, site := range unbounded {
			t.Errorf("%s reads its whole attributes bag at %s; a schema that declares every "+
				"attribute the executor reads cannot describe an unbounded read", e.name, site)
		}

		if schemaAdmitsUndeclaredKeys(t, e.name, e.schema) {
			t.Errorf("%s advertises a schema that admits an undeclared input; a bundled executor's schema is a "+
				"closed contract, so it sets additionalProperties to false, or to {\"readOnly\": true} when the "+
				"template names the outputs", e.name)
		}

		declared, readOnly := schemaProperties(t, e.name, e.schema)
		for _, key := range sortedKeys(read) {
			if stubSignature[key] {
				continue
			}
			if !declared[key] {
				t.Errorf("%s reads attribute %q and its expected-attributes schema does not declare it; "+
					"the schema declares every attribute the executor reads", e.name, key)
			}
		}
		for _, key := range sortedKeys(declared) {
			if readOnly[key] || stubSignature[key] {
				continue
			}
			if !read[key] {
				t.Errorf("%s declares attribute %q in its expected-attributes schema and reads it nowhere; "+
					"an input the executor never reads misleads every template that sets it", e.name, key)
			}
		}
	}
	t.Logf("checked all %d bundled executors under lib/services/executors, each against the attribute "+
		"keys its own source reads", len(executors))
}

// @decision: expected-attributes-schema-closed
func schemaAdmitsUndeclaredKeys(t *testing.T, name string, schema []byte) bool {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("%s: expected-attributes schema is not valid JSON: %v", name, err)
	}
	raw, present := doc["additionalProperties"]
	if !present {
		return true
	}
	if admits, isBool := raw.(bool); isBool {
		return admits
	}
	sub, isObject := raw.(map[string]any)
	if !isObject {
		return true
	}
	readOnly, _ := sub["readOnly"].(bool)
	return !readOnly
}

func schemaProperties(t *testing.T, name string, schema []byte) (declared, readOnly map[string]bool) {
	t.Helper()
	var doc struct {
		Properties map[string]struct {
			ReadOnly bool `json:"readOnly"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("%s: expected-attributes schema is not valid JSON: %v", name, err)
	}
	if len(doc.Properties) == 0 {
		t.Fatalf("%s: expected-attributes schema declares no properties", name)
	}
	declared = map[string]bool{}
	readOnly = map[string]bool{}
	for key, prop := range doc.Properties {
		declared[key] = true
		if prop.ReadOnly {
			readOnly[key] = true
		}
	}
	return declared, readOnly
}

func readStubModeSignature(t *testing.T) (constants map[string]string, funcKeys map[string]map[string]bool) {
	t.Helper()
	files := parsePackage(t, stubModePackageDir)
	constants = map[string]string{}
	for _, f := range files {
		collectStringConstants(f, constants)
	}
	funcKeys = map[string]map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			keys := map[string]bool{}
			collectBagReads(fn.Body, constants, nil, keys, nil)
			if len(keys) > 0 {
				funcKeys[fn.Name.Name] = keys
			}
		}
	}
	return constants, funcKeys
}

func attributeKeysRead(
	t *testing.T,
	dir string,
	stubConstants map[string]string,
	stubFuncKeys map[string]map[string]bool,
) (read map[string]bool, unbounded []string) {
	t.Helper()
	read = map[string]bool{}
	var roots []*ast.File
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		roots = append(roots, parseFile(t, path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}

	local := map[string]string{}
	for _, f := range roots {
		collectStringConstants(f, local)
	}
	constants := map[string]string{}
	for k, v := range local {
		constants[k] = v
	}
	for k, v := range stubConstants {
		constants["stubmode."+k] = v
	}

	fset := token.NewFileSet()
	_ = fset
	for _, f := range roots {
		var ranges []string
		collectBagReads(f, constants, stubFuncKeys, read, &ranges)
		unbounded = append(unbounded, ranges...)
	}
	sort.Strings(unbounded)
	return read, unbounded
}

func collectStringConstants(f *ast.File, into map[string]string) {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
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
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						into[name.Name] = s
					}
				}
			}
		}
	}
}

func collectBagReads(
	root ast.Node,
	constants map[string]string,
	stubFuncKeys map[string]map[string]bool,
	into map[string]bool,
	unbounded *[]string,
) {
	written := map[ast.Node]bool{}
	ast.Inspect(root, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			if idx, ok := lhs.(*ast.IndexExpr); ok {
				written[idx] = true
			}
		}
		return true
	})

	ast.Inspect(root, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.IndexExpr:
			if written[node] || !isDispatchBag(node.X) {
				return true
			}
			if key, ok := constantString(node.Index, constants); ok {
				into[key] = true
			}
		case *ast.RangeStmt:
			if isDispatchBag(node.X) && unbounded != nil && !copiesBagVerbatim(node) {
				*unbounded = append(*unbounded, exprName(node.X))
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "stubmode" {
				return true
			}
			for key := range stubFuncKeys[sel.Sel.Name] {
				into[key] = true
			}
		}
		return true
	})
}

func copiesBagVerbatim(node *ast.RangeStmt) bool {
	if node.Key == nil || node.Value == nil || node.Body == nil || len(node.Body.List) != 1 {
		return false
	}
	assign, ok := node.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	target, ok := assign.Lhs[0].(*ast.IndexExpr)
	if !ok {
		return false
	}
	return exprName(target.Index) == exprName(node.Key) && exprName(assign.Rhs[0]) == exprName(node.Value)
}

func isDispatchBag(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.Ident:
		return dispatchBagNames[e.Name]
	case *ast.SelectorExpr:
		return dispatchBagNames[e.Sel.Name]
	}
	return false
}

func exprName(x ast.Expr) string {
	switch e := x.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprName(e.X) + "." + e.Sel.Name
	}
	return "?"
}

func constantString(x ast.Expr, constants map[string]string) (string, bool) {
	switch e := x.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.Ident:
		v, ok := constants[e.Name]
		return v, ok
	case *ast.SelectorExpr:
		v, ok := constants[exprName(e)]
		return v, ok
	}
	return "", false
}

func parsePackage(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	var out []*ast.File
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		out = append(out, parseFile(t, path))
	}
	if len(out) == 0 {
		t.Fatalf("no source files under %s", dir)
	}
	return out
}

func parseFile(t *testing.T, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// @decision: expected-attributes-schema-closed
func TestSchemaAdmitsUndeclaredKeys_ReadsEachFormOfAdditionalProperties(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		admits bool
	}{
		{"absent", `{"type":"object","properties":{"url":{"type":"string"}}}`, true},
		{"false", `{"type":"object","properties":{"url":{"type":"string"}},"additionalProperties":false}`, false},
		{"true", `{"type":"object","properties":{"url":{"type":"string"}},"additionalProperties":true}`, true},
		{"read-only", `{"type":"object","properties":{"url":{"type":"string"}},"additionalProperties":{"readOnly":true}}`, false},
		{"typed", `{"type":"object","properties":{"url":{"type":"string"}},"additionalProperties":{"type":"string"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := schemaAdmitsUndeclaredKeys(t, "fixture", []byte(tc.schema))
			if got != tc.admits {
				t.Fatalf("schemaAdmitsUndeclaredKeys = %v, want %v. JSON Schema admits an undeclared property "+
					"unless the schema says otherwise, so a bundled executor closes its contract by writing the "+
					"key", got, tc.admits)
			}
		})
	}
}
