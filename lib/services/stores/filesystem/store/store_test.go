// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRejectsEmptyRoot(t *testing.T) {
	if _, err := New(Config{Root: ""}); err == nil {
		t.Fatal("New(\"\") should error; got nil")
	}
}

func TestOpenRejectsGlobMetacharacters(t *testing.T) {
	st, err := New(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []string{
		"foo/*.txt",
		"foo/?bar",
		"foo/[abc]/baz",
	}
	for _, sel := range cases {
		t.Run(sel, func(t *testing.T) {
			_, err := st.Open(context.Background(), "claim-1", sel)
			if err == nil {
				t.Fatalf("Open(%q): expected glob-rejection error; got nil", sel)
			}
			if !strings.Contains(err.Error(), "glob metacharacters") {
				t.Fatalf("Open(%q): error = %v, want contains 'glob metacharacters'", sel, err)
			}
		})
	}
}

func TestOpenRejectsEmptySelector(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "claim-1", "   "); err == nil {
		t.Fatal("Open(empty): expected error")
	}
}

func TestOpenEchoesPathUnderRoot(t *testing.T) {
	root := t.TempDir()
	st, _ := New(Config{Root: root})
	outcome, err := st.Open(context.Background(), "claim-1", "alpha/beta.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !outcome.Available {
		t.Fatalf("filesystem store should always return Available; got Unavailable")
	}

	var scope string
	if err := json.Unmarshal(outcome.Result.ClaimScope, &scope); err != nil {
		t.Fatalf("unmarshal scope: %v", err)
	}
	if scope != "alpha/beta.txt" {
		t.Fatalf("scope = %q, want %q", scope, "alpha/beta.txt")
	}

	var addr string
	if err := json.Unmarshal(outcome.Result.Address, &addr); err != nil {
		t.Fatalf("unmarshal address: %v", err)
	}
	want := filepath.Join(root, "alpha/beta.txt")
	if addr != want {
		t.Fatalf("address = %q, want %q", addr, want)
	}
}

func TestScopeByteEqualForSamePath(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	o1, _ := st.Open(context.Background(), "c1", "x/y.txt")
	o2, _ := st.Open(context.Background(), "c2", "x/y.txt")
	if string(o1.Result.ClaimScope) != string(o2.Result.ClaimScope) {
		t.Fatalf("same-path regions must be byte-equal; got %s vs %s", o1.Result.ClaimScope, o2.Result.ClaimScope)
	}
	o3, _ := st.Open(context.Background(), "c3", "x/z.txt")
	if string(o1.Result.ClaimScope) == string(o3.Result.ClaimScope) {
		t.Fatalf("different-path regions must differ; got identical %s", o1.Result.ClaimScope)
	}
}

func TestScopeCanonicalizationCollapsesEquivalentForms(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	equivalents := []string{"foo", "./foo", "foo/.", "./foo/.", "foo/"}
	first, err := st.Open(context.Background(), "c0", equivalents[0])
	if err != nil {
		t.Fatalf("Open(%q): %v", equivalents[0], err)
	}
	for i, sel := range equivalents[1:] {
		o, err := st.Open(context.Background(), fmt.Sprintf("c%d", i+1), sel)
		if err != nil {
			t.Fatalf("Open(%q): %v", sel, err)
		}
		if string(o.Result.ClaimScope) != string(first.Result.ClaimScope) {
			t.Fatalf("scope for %q differs from %q: %q vs %q",
				sel, equivalents[0], o.Result.ClaimScope, first.Result.ClaimScope)
		}
	}
}

func TestOpenRejectsPathTraversal(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	cases := []string{
		"../etc/passwd",
		"../../etc/passwd",
		"foo/../../etc/passwd",
		"./../escape",
		"..",
	}
	for _, sel := range cases {
		t.Run(sel, func(t *testing.T) {
			if _, err := st.Open(context.Background(), "claim-1", sel); err == nil {
				t.Fatalf("Open(%q): expected traversal rejection; got nil", sel)
			}
		})
	}
}

func TestOpenRejectsAbsolutePath(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "c1", "/etc/passwd"); err == nil {
		t.Fatal("Open(absolute): expected error")
	}
}

func TestCommitAbandonReleaseAreNoops(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	o, _ := st.Open(context.Background(), "claim-1", "x.txt")

	if err := st.Commit(context.Background(), "claim-1", o.Result.ClaimScope, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := st.Abandon(context.Background(), "claim-2", nil, nil); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if err := st.Release(context.Background(), "claim-3", nil, nil); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestCapabilitiesIsSyncEnvelope(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	caps := st.Capabilities()
	if len(caps.WriteSemanticsAllowed) != 1 || string(caps.WriteSemanticsAllowed[0]) != "sync" {
		t.Fatalf("expected envelope [sync], got %v", caps.WriteSemanticsAllowed)
	}
}
