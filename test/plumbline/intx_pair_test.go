// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type intxInterface struct {
	name     string
	methods  map[string]bool
	embedded []string
}

// @decision: intx-suffix-convention
func TestNoPersistenceMethodCoexistsWithInTxSibling(t *testing.T) {
	repoRoot := findRepoRoot(t)
	persistenceRoot := filepath.Join(repoRoot, "lib", "foundation", "persistence")

	type key struct{ pkg, receiver, base string }
	methods := map[key]map[bool]string{}

	type recvKey struct{ pkg, receiver string }
	receiverMethodSets := map[recvKey]map[string]bool{}

	interfaces := map[string]*intxInterface{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(persistenceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, decl := range file.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok {
				for _, sp := range gd.Specs {
					ts, ok := sp.(*ast.TypeSpec)
					if !ok {
						continue
					}
					iface, ok := ts.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					info := &intxInterface{name: ts.Name.Name, methods: map[string]bool{}}
					for _, m := range iface.Methods.List {
						if len(m.Names) == 0 {
							if id, ok := m.Type.(*ast.Ident); ok {
								info.embedded = append(info.embedded, id.Name)
							}
							continue
						}
						for _, n := range m.Names {
							info.methods[n.Name] = true
						}
					}
					interfaces[info.name] = info
				}
				continue
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			receiver := receiverTypeName(fn.Recv.List[0].Type)
			name := fn.Name.Name
			rk := recvKey{pkg: file.Name.Name, receiver: receiver}
			if receiverMethodSets[rk] == nil {
				receiverMethodSets[rk] = map[string]bool{}
			}
			receiverMethodSets[rk][name] = true
			base, isInTx := strings.CutSuffix(name, "InTx")
			if base == "" {
				base = name
			}
			k := key{pkg: file.Name.Name, receiver: receiver, base: strings.ToLower(base)}
			if methods[k] == nil {
				methods[k] = map[bool]string{}
			}
			methods[k][isInTx] = name
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", persistenceRoot, err)
	}

	capabilitySplitSets := expandCapabilitySplitInterfaces(interfaces)

	implementsSplitContractWith := func(pkg, receiver, inTxMethod string) bool {
		set := receiverMethodSets[recvKey{pkg: pkg, receiver: receiver}]
		for _, ifaceSet := range capabilitySplitSets {
			if !ifaceSet[inTxMethod] {
				continue
			}
			covered := true
			for m := range ifaceSet {
				if !set[m] {
					covered = false
					break
				}
			}
			if covered {
				return true
			}
		}
		return false
	}

	for k, variants := range methods {
		plain, hasPlain := variants[false]
		inTx, hasInTx := variants[true]
		if !hasPlain || !hasInTx {
			continue
		}
		if implementsSplitContractWith(k.pkg, k.receiver, inTx) {
			continue
		}
		t.Errorf("persistence package %q has the retired paired idiom on %s: %s coexists with %s — "+
			"collapse to one method taking an optional transaction parameter (the InTx suffix alone means "+
			"\"requires an open transaction\"; a live pair is a copy source for the retired idiom); the only "+
			"exemption is a receiver that implements a capability-split interface declaring the InTx method",
			k.pkg, k.receiver, plain, inTx)
	}
}

func expandCapabilitySplitInterfaces(interfaces map[string]*intxInterface) map[string]map[string]bool {
	var expand func(name string, seen map[string]bool) map[string]bool
	expand = func(name string, seen map[string]bool) map[string]bool {
		out := map[string]bool{}
		if seen[name] {
			return out
		}
		seen[name] = true
		info, ok := interfaces[name]
		if !ok {
			return out
		}
		for m := range info.methods {
			out[m] = true
		}
		for _, emb := range info.embedded {
			for m := range expand(emb, seen) {
				out[m] = true
			}
		}
		return out
	}
	sets := map[string]map[string]bool{}
	for name := range interfaces {
		full := expand(name, map[string]bool{})
		hasInTx := false
		for m := range full {
			if strings.HasSuffix(m, "InTx") {
				hasInTx = true
				break
			}
		}
		if hasInTx {
			sets[name] = full
		}
	}
	return sets
}

func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return receiverTypeName(e.X)
	}
	return ""
}
