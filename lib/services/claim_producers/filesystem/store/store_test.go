// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func newTestPickPolicy(sub string) *PickPolicy {
	return &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
}

func TestNew_RejectsSelectorWithPathSeparator(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []string{"a/b", "@a/b", "a\\b", "..", "@.."}
	for _, sel := range cases {
		t.Run(sel, func(t *testing.T) {
			_, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{sel: newTestPickPolicy(sub)}})
			if err == nil {
				t.Fatalf("New with selector %q: expected rejection, got nil", sel)
			}
		})
	}
}

func TestNew_RejectsSelectorCollisionAfterAtTrim(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{
		"@ring": newTestPickPolicy(sub),
		"ring":  newTestPickPolicy(sub),
	}})
	if err == nil {
		t.Fatal("New with selectors \"@ring\" and \"ring\" (same state directory once '@' is stripped): expected rejection, got nil")
	}
}

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

func TestOpenRejectsStoreStateDirectory(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	cases := []string{
		".fs-store",
		".fs-store/r/available/alpha",
		".fs-store/r/in_progress/alpha.c1.123",
	}
	for _, sel := range cases {
		t.Run(sel, func(t *testing.T) {
			if _, err := st.Open(context.Background(), "claim-1", sel); err == nil {
				t.Fatalf("Open(%q): expected rejection of the store's internal state directory; got nil", sel)
			}
		})
	}
}

func TestOpen_SymlinkEscapingRootIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	st, err := New(Config{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := st.Open(context.Background(), "claim-1", "escape/secret"); err == nil {
		t.Fatal("Open through a symlink that escapes the store root: expected rejection, got nil")
	}
}

func TestOpenRejectsAbsolutePath(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "c1", "/etc/passwd"); err == nil {
		t.Fatal("Open(absolute): expected error")
	}
}

func TestOpenRejectsInvalidClaimID(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	cases := []string{"", "a/b", "a..b", "a.b", "../escape", "a\\b"}
	for _, id := range cases {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			if _, err := st.Open(context.Background(), id, "x.txt"); err == nil {
				t.Fatalf("Open(claimID=%q): expected error; got nil", id)
			}
		})
	}
}

func TestCommitAbandonReleaseAreNoops(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	o, _ := st.Open(context.Background(), "claim-1", "x.txt")

	if err := st.Commit(context.Background(), "claim-1", o.Result.ClaimScope, o.Result.Address, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := st.Abandon(context.Background(), "claim-2", nil, nil, ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if err := st.Release(context.Background(), "claim-3", nil, nil, ""); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestBatchPopDoesNotPopulateClaimsMap(t *testing.T) {
	root := t.TempDir()
	queueDir := filepath.Join(root, "queue")
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(queueDir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	pp := &PickPolicy{
		Root:              "queue",
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@queue": pp}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := len(st.claims)
	items, err := st.BatchPop(context.Background(), "@queue", []string{"id-1", "id-2"})
	if err != nil {
		t.Fatalf("BatchPop: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("BatchPop: got %d items, want 2", len(items))
	}
	if got := len(st.claims); got != before {
		t.Errorf("BatchPop populated s.claims map (before=%d, after=%d) — would leak entries because the server-internal claim IDs never get released by rimsky", before, got)
	}
}

func TestBatchPopRejectsTraversalClaimID(t *testing.T) {
	root := t.TempDir()
	queueDir := filepath.Join(root, "queue")
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(queueDir, "a"), 0o755); err != nil {
		t.Fatalf("mkdir a: %v", err)
	}
	pp := &PickPolicy{
		Root:              "queue",
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@queue": pp}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := st.BatchPop(context.Background(), "@queue", []string{"../escape"}); err == nil {
		t.Fatal("BatchPop with traversal claim_id: expected error, got nil")
	}
	inProgDir := filepath.Join(PolicyStateDir(root, "@queue"), "in_progress")
	entries, err := os.ReadDir(inProgDir)
	if err != nil {
		t.Fatalf("readdir in_progress: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("in_progress must remain empty after a rejected BatchPop, got %v", entries)
	}
}

func TestNew_LedgerMaxRecordsConfigured(t *testing.T) {
	st, err := New(Config{Root: t.TempDir(), LedgerMaxRecords: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := st.Open(context.Background(), "claim-1", "x.txt"); err != nil {
		t.Fatalf("Open claim-1: %v", err)
	}
	if err := st.Commit(context.Background(), "claim-1", nil, nil, ""); err != nil {
		t.Fatalf("Commit claim-1: %v", err)
	}
	if _, err := st.Open(context.Background(), "claim-2", "y.txt"); err != nil {
		t.Fatalf("Open claim-2: %v", err)
	}
	if _, err := st.Open(context.Background(), "claim-3", "z.txt"); err != nil {
		t.Fatalf("Open claim-3: %v", err)
	}
	if _, ok := st.Ledger().Get("claim-1"); ok {
		t.Fatal("closed claim-1 should have been evicted once the configured cap (2) was exceeded")
	}
	if _, ok := st.Ledger().Get("claim-2"); !ok {
		t.Error("open claim-2 must survive eviction")
	}
	if _, ok := st.Ledger().Get("claim-3"); !ok {
		t.Error("open claim-3 must survive eviction")
	}
}

func TestCapabilitiesIsSyncEnvelope(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	caps := st.Capabilities()
	if len(caps.WriteSemanticsAllowed) != 1 || string(caps.WriteSemanticsAllowed[0]) != "sync" {
		t.Fatalf("expected envelope [sync], got %v", caps.WriteSemanticsAllowed)
	}
	if !caps.SupportsSplitScope {
		t.Error("store.Capabilities() must advertise SupportsSplitScope=true; the gRPC server derives its own capability response from this single source")
	}
	found := false
	for _, c := range caps.DeclaredErrorClasses {
		if c == RootUnavailableClass {
			found = true
		}
	}
	if !found {
		t.Errorf("store.Capabilities() DeclaredErrorClasses = %v, want to contain %q", caps.DeclaredErrorClasses, RootUnavailableClass)
	}
}

func TestOpenScoped_CaseFoldConsistency(t *testing.T) {
	st, err := New(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	o1, err := st.openScoped("c1", "Docs/A")
	if err != nil {
		t.Fatalf("openScoped Docs/A: %v", err)
	}
	o2, err := st.openScoped("c2", "docs/a")
	if err != nil {
		t.Fatalf("openScoped docs/a: %v", err)
	}
	equal := string(o1.Result.ClaimScope) == string(o2.Result.ClaimScope)
	if equal != st.caseFold {
		t.Errorf("scope equality for Docs/A vs docs/a = %v; must equal caseFold = %v (folding gates the byte-equal conflict predicate on case-insensitive filesystems)", equal, st.caseFold)
	}
	if st.caseFold {
		var scope string
		if err := json.Unmarshal(o1.Result.ClaimScope, &scope); err != nil {
			t.Fatalf("scope not a JSON string: %v", err)
		}
		if scope != strings.ToLower(scope) {
			t.Errorf("case-insensitive root: scope %q must be folded to lowercase", scope)
		}
	}
}
