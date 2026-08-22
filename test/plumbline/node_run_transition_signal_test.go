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
)

type transitionSite struct {
	state     string
	reason    string
	hasSignal bool
	where     string
}

type heldVerdict int

const (
	heldOK heldVerdict = iota
	heldMissingSignal
	heldSpuriousSignal
	heldUnclassifiedReason
)

// @decision: held-as-state-not-phase
// @concept: signal
func heldRule(s transitionSite) heldVerdict {
	switch s.reason {
	case "ReasonHandlerHeld":
		if s.hasSignal {
			return heldOK
		}
		return heldMissingSignal
	case "ReasonFanoutDispatched":
		if s.hasSignal {
			return heldSpuriousSignal
		}
		return heldOK
	}
	return heldUnclassifiedReason
}

// @concept: signal
func TestEveryNodeRunTransitionNamesExactlyOneSignal(t *testing.T) {
	repoRoot := findRepoRoot(t)
	sites := nodeRunTransitionSites(t, repoRoot)
	if len(sites) == 0 {
		t.Fatal("found no node-run transition site; the check would pass over an empty population")
	}

	signalBearing := map[string]bool{
		"NodeStateFresh":  true,
		"NodeStateFailed": true,
		"NodeStateParked": true,
	}
	runnable := map[string]bool{
		"NodeStateStale":   true,
		"NodeStateRunning": true,
	}

	var missing, spurious, unknown []string
	byState := map[string]int{}
	heldSites := 0
	for _, s := range sites {
		byState[s.state]++
		switch {
		case signalBearing[s.state]:
			if !s.hasSignal {
				missing = append(missing, s.where+" -> "+s.state)
			}
		case runnable[s.state]:
			if s.hasSignal {
				spurious = append(spurious, s.where+" -> "+s.state)
			}
		case s.state == "NodeStateHeld":
			heldSites++
			switch heldRule(s) {
			case heldMissingSignal:
				missing = append(missing, s.where+" -> held ("+s.reason+")")
			case heldSpuriousSignal:
				spurious = append(spurious, s.where+" -> held ("+s.reason+")")
			case heldUnclassifiedReason:
				unknown = append(unknown, s.where+" -> held ("+s.reason+")")
			case heldOK:
			}
		default:
			unknown = append(unknown, s.where+" -> "+s.state)
		}
	}
	sort.Strings(missing)
	sort.Strings(spurious)
	sort.Strings(unknown)

	if len(missing) > 0 {
		t.Errorf("%d of %d node-run transition sites settle a run without naming a signal; "+
			"a signal is the one emission shape for a transition that affects a node-run, so a "+
			"settling transition that emits none is invisible to every subscriber and to the audit "+
			"ledger:\n%s", len(missing), len(sites), strings.Join(missing, "\n"))
	}
	if len(spurious) > 0 {
		t.Errorf("%d node-run transition sites name a signal on a transition into a runnable state; "+
			"only a transition that settles or suspends a run emits one:\n%s",
			len(spurious), strings.Join(spurious, "\n"))
	}
	if len(unknown) > 0 {
		t.Errorf("%d node-run transition sites write a state, or a reason for entering held, that this check "+
			"does not classify; extend the check when the state machine gains a state, and extend heldRule when "+
			"a new reason moves a run into held:\n%s",
			len(unknown), strings.Join(unknown, "\n"))
	}

	states := make([]string, 0, len(byState))
	for s := range byState {
		states = append(states, s+"="+strconv.Itoa(byState[s]))
	}
	sort.Strings(states)
	t.Logf("checked all %d product calls to Nodes().UpdateState under lib/runtime; each carries at most one "+
		"settling signal type, because the state write takes exactly one: %s. %d of them enter held, and the "+
		"reason discriminates: a held transition that settles the dispatch names a signal, one that suspends a "+
		"parent while its children run names none", len(sites), strings.Join(states, " "), heldSites)
}

func nodeRunTransitionSites(t *testing.T, repoRoot string) []transitionSite {
	t.Helper()
	fset := token.NewFileSet()
	var out []transitionSite
	runtimeDir := filepath.Join(repoRoot, "lib", "runtime")
	err := filepath.WalkDir(runtimeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		rel, _ := filepath.Rel(repoRoot, path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isUpdateStateCall(call) {
					return true
				}
				if len(call.Args) != 6 {
					t.Fatalf("%s:%d: Nodes().UpdateState takes 6 arguments; got %d — the check reads the "+
						"state at argument 3, the reason at argument 4, and the settling signal type at argument 5",
						rel, fset.Position(call.Pos()).Line, len(call.Args))
				}
				where := rel + ":" + strconv.Itoa(fset.Position(call.Pos()).Line)
				reason := strings.Join(namesWrittenAt(fn, call.Args[3]), "|")
				for _, state := range namesWrittenAt(fn, call.Args[2]) {
					out = append(out, transitionSite{
						state:     state,
						reason:    reason,
						hasSignal: !isNilIdent(call.Args[4]),
						where:     where,
					})
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", runtimeDir, err)
	}
	return out
}

func isUpdateStateCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "UpdateState" {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	nodes, ok := inner.Fun.(*ast.SelectorExpr)
	return ok && nodes.Sel.Name == "Nodes"
}

func isNilIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "nil"
}

func namesWrittenAt(fn *ast.FuncDecl, expr ast.Expr) []string {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return []string{sel.Sel.Name}
	}
	id, ok := expr.(*ast.Ident)
	if !ok {
		return []string{"<computed>"}
	}
	seen := map[string]struct{}{}
	record := func(lhs, rhs []ast.Expr) {
		for i, l := range lhs {
			li, isIdent := l.(*ast.Ident)
			if !isIdent || li.Name != id.Name || i >= len(rhs) {
				continue
			}
			if sel, isSel := rhs[i].(*ast.SelectorExpr); isSel {
				seen[sel.Sel.Name] = struct{}{}
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			record(node.Lhs, node.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, 0, len(node.Names))
			for _, nm := range node.Names {
				lhs = append(lhs, nm)
			}
			record(lhs, node.Values)
		}
		return true
	})
	if len(seen) == 0 {
		return []string{"<computed>"}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
