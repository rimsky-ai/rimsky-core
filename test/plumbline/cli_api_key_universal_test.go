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

type cliPackage struct {
	name     string
	dir      []string
	prefixes []string
}

var cliVerbPackages = []cliPackage{
	{name: "cli", dir: []string{"cmd", "rimsky", "cli"}, prefixes: []string{"Run", "runAgent"}},
	{name: "compose", dir: []string{"cmd", "rimsky", "cli", "compose"}, prefixes: []string{"Run"}},
	{name: "main", dir: []string{"cmd", "rimsky"}, prefixes: []string{"runConformance"}},
}

var apiKeyUniversalExceptions = map[string]string{
	"cli.RunCtxList":                         "context management reads and writes local state only",
	"cli.RunCtxUse":                          "context management reads and writes local state only",
	"cli.RunCtxAdd":                          "context management reads and writes local state only",
	"cli.RunCtxRm":                           "context management reads and writes local state only",
	"cli.RunCtxCurrent":                      "context management reads and writes local state only",
	"cli.runAgentStatus":                     "the host-agent status verb reads local state only",
	"cli.runAgentStop":                       "the host-agent stop verb writes local state only",
	"cli.runAgentStart":                      "the host-agent start verb hands the key to the proxy under its own flag",
	"cli.RunAuthLogin":                       "the interactive login verb reads the key from the terminal and stores it",
	"compose.RunComposeRun":                  "the compose one-shot self-hosts its stack and reaches it over loopback",
	"compose.RunTemplateRun":                 "the ephemeral-run verb reaches its self-hosted stack over loopback on the compose one-shot's own machinery; its remote branch delegates to cli.RunRunRemote, which this test checks",
	"main.runConformanceExecutor":            "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformanceClaimProducer":       "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformancePublisher":           "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformanceValidation":          "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformanceDataProcessing":      "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformanceBlobBackend":         "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformanceLifecycleSubscriber": "a conformance verb dials the service under test over that service's own protocol",
	"main.runConformanceProbe":               "a conformance verb dials the service under test over that service's own protocol",
}

type funcNode struct {
	calls  map[string]bool
	tokens map[string]bool
	idents map[string]bool
}

type cliCallGraph struct {
	funcs map[string]*funcNode
	verbs []string
}

func loadCLICallGraph(t *testing.T, repoRoot string) *cliCallGraph {
	t.Helper()
	g := &cliCallGraph{funcs: map[string]*funcNode{}}
	verbSet := map[string]bool{}
	for _, pkg := range cliVerbPackages {
		dir := filepath.Join(append([]string{repoRoot}, pkg.dir...)...)
		files := parsePackageFiles(t, dir, pkg.name)
		if len(files) == 0 {
			t.Fatalf("package %q has no non-test source files in %s", pkg.name, dir)
		}
		for _, file := range files {
			aliases := importAliases(file)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				key := pkg.name + "." + fn.Name.Name
				node := collectCalls(fn, pkg.name, aliases)
				if existing := g.funcs[key]; existing != nil {
					mergeInto(existing, node)
				} else {
					g.funcs[key] = node
				}
				if hasAnyPrefix(fn.Name.Name, pkg.prefixes) {
					verbSet[key] = true
				}
			}
		}
	}
	for verb := range verbSet {
		g.verbs = append(g.verbs, verb)
	}
	sort.Strings(g.verbs)
	return g
}

func parsePackageFiles(t *testing.T, dir, pkgName string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), perr)
		}
		if file.Name.Name != pkgName {
			continue
		}
		files = append(files, file)
	}
	return files
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) && strings.ToUpper(name[len(p):len(p)+1]) == name[len(p):len(p)+1] {
			return true
		}
	}
	return false
}

func importAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			aliases[imp.Name.Name] = name
			continue
		}
		aliases[name] = name
	}
	return aliases
}

func collectCalls(fn *ast.FuncDecl, pkgName string, aliases map[string]string) *funcNode {
	node := &funcNode{calls: map[string]bool{}, tokens: map[string]bool{}, idents: map[string]bool{}}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			node.idents[id.Name] = true
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			node.idents[sel.Sel.Name] = true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			node.tokens[fun.Name] = true
			node.calls[pkgName+"."+fun.Name] = true
		case *ast.SelectorExpr:
			node.tokens[fun.Sel.Name] = true
			if x, ok := fun.X.(*ast.Ident); ok {
				if target, ok := aliases[x.Name]; ok {
					node.calls[target+"."+fun.Sel.Name] = true
				}
			}
		}
		return true
	})
	return node
}

func mergeInto(dst, src *funcNode) {
	for k := range src.calls {
		dst.calls[k] = true
	}
	for k := range src.tokens {
		dst.tokens[k] = true
	}
	for k := range src.idents {
		dst.idents[k] = true
	}
}

func (g *cliCallGraph) reachIdents(verb string, stopAt map[string]bool) map[string]bool {
	idents := map[string]bool{}
	seen := map[string]bool{verb: true}
	queue := []string{verb}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		node := g.funcs[key]
		if node == nil {
			continue
		}
		for id := range node.idents {
			idents[id] = true
		}
		for callee := range node.calls {
			if seen[callee] || g.isVerb(callee) || stopAt[callee] {
				continue
			}
			seen[callee] = true
			queue = append(queue, callee)
		}
	}
	return idents
}

func (g *cliCallGraph) isVerb(key string) bool {
	for _, v := range g.verbs {
		if v == key {
			return true
		}
	}
	return false
}

func (g *cliCallGraph) reach(verb string) map[string]bool {
	tokens := map[string]bool{}
	seen := map[string]bool{verb: true}
	queue := []string{verb}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		node := g.funcs[key]
		if node == nil {
			continue
		}
		for tok := range node.tokens {
			tokens[tok] = true
		}
		for callee := range node.calls {
			if seen[callee] || g.isVerb(callee) {
				continue
			}
			seen[callee] = true
			queue = append(queue, callee)
		}
	}
	return tokens
}

func anyToken(tokens map[string]bool, names ...string) bool {
	for _, n := range names {
		if tokens[n] {
			return true
		}
	}
	return false
}

// @concept: rimsky
// @concept: api-key
func TestEveryVerbBuildingAControlAPIClientSendsTheResolvedKey(t *testing.T) {
	g := loadCLICallGraph(t, findRepoRoot(t))

	for name := range apiKeyUniversalExceptions {
		if g.funcs[name] == nil {
			t.Errorf("the exception list names %s, which no longer exists: an exception that covers nothing "+
				"hides the verb it was written for", name)
		}
	}

	var dialing, exempt, local, inherited []string
	for _, verb := range g.verbs {
		tokens := g.reach(verb)
		if !anyToken(tokens, "NewClient", "NewClientWithKey") {
			local = append(local, verb)
			continue
		}
		if _, ok := apiKeyUniversalExceptions[verb]; ok {
			exempt = append(exempt, verb)
			continue
		}
		dialing = append(dialing, verb)
		if !anyToken(tokens, "SetAPIKey", "NewClientWithKey") {
			t.Errorf("%s dials the control API but never sends a resolved key: every verb that dials the "+
				"control-api sends the resolved key as its authentication token", verb)
		}
		if !anyToken(tokens, "NewFlagSet") {
			inherited = append(inherited, verb)
			continue
		}
		if !anyToken(tokens, "RegisterCommonFlags", "RegisterAPIKeyFlag") {
			t.Errorf("%s dials the control API but defines no api-key flag: every verb that dials the "+
				"control-api accepts an API-key flag", verb)
		}
	}

	if len(dialing) == 0 {
		t.Fatalf("no control-api-dialing verb found across %d verbs: the check inspected nothing", len(g.verbs))
	}
	t.Logf("checked %d control-api-dialing verbs: %s", len(dialing), strings.Join(dialing, ", "))
	t.Logf("%d dialing verbs stand outside the rule by the concept's own exceptions: %s", len(exempt), strings.Join(exempt, ", "))
	t.Logf("%d verbs dial no control API: %s", len(local), strings.Join(local, ", "))
	t.Logf("%d of them own no flag set and take their key flag from the verb that calls them: %s",
		len(inherited), strings.Join(inherited, ", "))
}
