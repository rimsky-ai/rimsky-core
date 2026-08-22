// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const advisoryLockerSource = "advisory_locker.go"

// @concept: advisory-lock
func TestPinnedAdvisoryLockKeysArePairwiseDistinctWithinTheirLockSpace(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), advisoryLockerSource, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", advisoryLockerSource, err)
	}
	declared := declaredAdvisoryLockKeys(file)
	if len(declared) == 0 {
		t.Fatalf("%s declares no advisory-lock key constants", advisoryLockerSource)
	}
	sessionKeys, transactionKeys := advisoryLockKeysByLockSpace(t, file, declared)

	requireDistinctAdvisoryKeys(t, "session-scoped (pg_advisory_lock)", sessionKeys)
	requireDistinctAdvisoryKeys(t, "transaction-scoped class (pg_advisory_xact_lock)", transactionKeys)

	used := append(sortedNames(sessionKeys), sortedNames(transactionKeys)...)
	sort.Strings(used)
	want := sortedNames(declared)
	if strings.Join(used, ",") != strings.Join(want, ",") {
		t.Fatalf("every declared advisory-lock key must reach a lock call: declared %v, used %v", want, used)
	}
	t.Logf("checked %d session-scoped keys and %d transaction-scoped classes, the whole population %s declares.",
		len(sessionKeys), len(transactionKeys), advisoryLockerSource)
}

func declaredAdvisoryLockKeys(file *ast.File) map[string]int64 {
	out := map[string]int64{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != len(vs.Values) {
				continue
			}
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					continue
				}
				v, err := strconv.ParseInt(lit.Value, 10, 64)
				if err != nil {
					continue
				}
				out[name.Name] = v
			}
		}
	}
	return out
}

func advisoryLockKeysByLockSpace(
	t *testing.T, file *ast.File, declared map[string]int64,
) (session, transaction map[string]int64) {
	t.Helper()
	session = map[string]int64{}
	transaction = map[string]int64{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for i, arg := range call.Args {
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			sql, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.Contains(sql, "advisory") {
				continue
			}
			if i+1 >= len(call.Args) {
				continue
			}
			name := advisoryKeyIdent(call.Args[i+1])
			if name == "" {
				continue
			}
			value, known := declared[name]
			if !known {
				t.Fatalf("lock call passes %s, which %s does not declare as a constant", name, advisoryLockerSource)
			}
			switch {
			case strings.Contains(sql, "pg_advisory_xact_lock($1, hashtext($2))"):
				transaction[name] = value
			case strings.Contains(sql, "advisory_lock($1)"), strings.Contains(sql, "advisory_unlock($1)"):
				session[name] = value
			default:
				t.Fatalf("lock call %q sits in no known advisory-lock space", sql)
			}
		}
		return true
	})
	return session, transaction
}

func advisoryKeyIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.CallExpr:
		if len(e.Args) == 1 {
			return advisoryKeyIdent(e.Args[0])
		}
	}
	return ""
}

func requireDistinctAdvisoryKeys(t *testing.T, space string, keys map[string]int64) {
	t.Helper()
	if len(keys) == 0 {
		t.Fatalf("no advisory-lock key reaches the %s space", space)
	}
	seen := map[int64]string{}
	for _, name := range sortedNames(keys) {
		key := keys[name]
		if prior, clash := seen[key]; clash {
			t.Fatalf("%s keys %s and %s share the value %d; two locks sharing a key exclude each other",
				space, prior, name, key)
		}
		seen[key] = name
	}
}

func sortedNames(keys map[string]int64) []string {
	out := make([]string, 0, len(keys))
	for name := range keys {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
