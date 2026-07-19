// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

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

var inertClaimBytesFields = map[string]bool{"Address": true, "Payload": true, "ClaimScope": true}

// @concept: inertness
func TestClaimContentInertness_ReadSitesAreEnumerated(t *testing.T) {
	got := findInertClaimByteReadSites(t, ".")
	want := map[string]bool{
		"makeHeldClaimHandle": true,
		"makeClaimHandle":     true,
		"AcquireSubClaims":    true,
	}
	for fn := range want {
		if !got[fn] {
			t.Errorf("expected claim-content inert-byte read site %q (json.Unmarshal/json.Valid over "+
				"claim Address/Payload/ClaimScope) not found in lib/runtime; it was removed or renamed", fn)
		}
	}
	for fn := range got {
		if !want[fn] {
			names := make([]string, 0, len(got))
			for n := range got {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Errorf("new claim-content inert-byte read site %q found (json.Unmarshal/json.Valid over a claim "+
				"Address/Payload/ClaimScope field); claim content is byte-opaque inert (invariant 20) — read "+
				"only at the enumerated sanctioned sites %v. Either this is a legitimate new sanctioned site "+
				"(update this test's allowlist and the inertness concept doc) or it's a stray inspection site "+
				"that must be removed", fn, names)
		}
	}
}

func findInertClaimByteReadSites(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			funcName := fd.Name.Name
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "json" {
					return true
				}
				if sel.Sel.Name != "Unmarshal" && sel.Sel.Name != "Valid" {
					return true
				}
				if len(call.Args) == 0 {
					return true
				}
				arg, ok := call.Args[0].(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if inertClaimBytesFields[arg.Sel.Name] {
					out[funcName] = true
				}
				return true
			})
		}
	}
	return out
}
