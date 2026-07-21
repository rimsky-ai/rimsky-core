// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func newServerWithStore(t *testing.T) (*Server, *fsstore.Store, string) {
	t.Helper()
	root := t.TempDir()
	st, err := fsstore.New(fsstore.Config{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &Server{store: st}, st, root
}

func newServerWithPickPolicy(t *testing.T, sub string, onCommit action.Action) (*Server, *fsstore.Store, string) {
	return newServerWithPickPolicyAndSync(t, sub, onCommit, "on_open")
}

func newServerWithPickPolicyAndSync(t *testing.T, sub string, onCommit action.Action, syncStrategy string) (*Server, *fsstore.Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pp := &fsstore.PickPolicy{
		Root:              sub,
		OnCommit:          onCommit,
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      syncStrategy,
	}
	st, err := fsstore.New(fsstore.Config{
		Root:         root,
		PickPolicies: map[string]*fsstore.PickPolicy{"@queue": pp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &Server{store: st}, st, root
}

func TestSplitScope_ListShape(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	_, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("Open parent: %v", err)
	}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"list":[{"key":"a","payload":{"v":1}},{"key":"b","payload":{"v":2}}]}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got, want := len(resp.SubScopes), 2; got != want {
		t.Fatalf("SubScopes count = %d, want %d", got, want)
	}
	wantPayloads := map[string][]byte{
		"a": []byte(`{"v":1}`),
		"b": []byte(`{"v":2}`),
	}
	for _, sub := range resp.SubScopes {
		want, ok := wantPayloads[sub.PartitionKey]
		if !ok {
			t.Errorf("unexpected PartitionKey: %q", sub.PartitionKey)
			continue
		}
		if !bytes.Equal(sub.Payload, want) {
			t.Errorf("PartitionKey=%q: Payload = %s, want %s",
				sub.PartitionKey, string(sub.Payload), string(want))
		}
		if len(sub.Address) != 0 {
			t.Errorf("PartitionKey=%q: Address should be empty for list shape, got %s",
				sub.PartitionKey, string(sub.Address))
		}
		var scopePath string
		if err := json.Unmarshal(sub.ClaimScopeData, &scopePath); err != nil {
			t.Errorf("PartitionKey=%q: ClaimScopeData not a JSON string: %v", sub.PartitionKey, err)
			continue
		}
		if filepath.IsAbs(scopePath) {
			t.Errorf("PartitionKey=%q: ClaimScopeData path %q must be store-root-relative, not absolute", sub.PartitionKey, scopePath)
		}
		if want := filepath.Join("parent", "_list", sub.PartitionKey); scopePath != want {
			t.Errorf("PartitionKey=%q: ClaimScopeData = %q, want root-relative %q", sub.PartitionKey, scopePath, want)
		}
	}
}

func TestSplitScope_ListDuplicateKeyRejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"list":[{"key":"a","payload":{}},{"key":"a","payload":{}}]}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for duplicate list key, got nil")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("SplitScope: expected InvalidArgument, got %v", err)
	}
}

func TestSplitScope_ListTraversalKeyRejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	cases := []string{"../escape", "a/../b", "..", ".", "sub/dir"}
	for _, key := range cases {
		key := key
		t.Run(key, func(t *testing.T) {
			req := &genv1.SplitScopeRequest{
				ClaimHandleId:    "p1",
				PartitionRequest: []byte(fmt.Sprintf(`{"list":[{"key":%q,"payload":{}}]}`, key)),
			}
			_, err := srv.SplitScope(context.Background(), req)
			if err == nil {
				t.Fatalf("SplitScope: expected error for traversal key %q, got nil", key)
			}
			if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
				t.Fatalf("SplitScope: expected InvalidArgument for key %q, got %v", key, err)
			}
		})
	}
}

func TestSplitScope_ListTraversalAndDuplicateKeysCollapseToDistinctScopes(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"list":[{"key":"a","payload":{}},{"key":"b","payload":{}}]}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	seen := map[string]bool{}
	for _, sub := range resp.SubScopes {
		if seen[string(sub.ClaimScopeData)] {
			t.Fatalf("SplitScope: overlapping ClaimScopeData %s across sibling sub-scopes", sub.ClaimScopeData)
		}
		seen[string(sub.ClaimScopeData)] = true
	}
}

func TestSplitScope_BatchPickShape(t *testing.T) {
	srv, _, root := newServerWithPickPolicy(t, "queue", action.Action{Kind: action.Recycle})
	for _, name := range []string{"f1", "f2", "f3", "f4", "f5"} {
		if err := os.MkdirAll(filepath.Join(root, "queue", name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "parent",
		Selector: "@queue",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "parent",
		PartitionRequest: []byte(`{"batch_pick":{"max_items":3}}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got, want := len(resp.SubScopes), 3; got != want {
		t.Fatalf("SubScopes count = %d, want %d", got, want)
	}
	seen := make(map[string]bool)
	for _, sub := range resp.SubScopes {
		if sub.PartitionKey == "" {
			t.Errorf("PartitionKey should be non-empty")
		}
		if seen[sub.PartitionKey] {
			t.Errorf("duplicate PartitionKey %q", sub.PartitionKey)
		}
		seen[sub.PartitionKey] = true
		if len(sub.Address) == 0 {
			t.Errorf("PartitionKey=%q: Address should carry path bytes", sub.PartitionKey)
		}
		var addrPath string
		if err := json.Unmarshal(sub.Address, &addrPath); err != nil {
			t.Errorf("PartitionKey=%q: Address not JSON string: %v", sub.PartitionKey, err)
			continue
		}
		if filepath.Base(addrPath) != sub.PartitionKey {
			t.Errorf("PartitionKey=%q: Address basename %q does not match", sub.PartitionKey, filepath.Base(addrPath))
		}
	}
}

func TestSplitScope_BatchPickFewerAvailable(t *testing.T) {
	srv, _, root := newServerWithPickPolicy(t, "queue", action.Action{Kind: action.Recycle})
	for _, name := range []string{"f1", "f2"} {
		if err := os.MkdirAll(filepath.Join(root, "queue", name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "parent",
		Selector: "@queue",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "parent",
		PartitionRequest: []byte(`{"batch_pick":{"max_items":5}}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got, want := len(resp.SubScopes), 1; got != want {
		t.Fatalf("SubScopes count = %d, want %d (parent already holds 1 of 2)", got, want)
	}
}

func TestSplitScope_BatchPick_DirectAddressParentWithoutPolicyRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "direct"), 0o755); err != nil {
		t.Fatalf("mkdir direct: %v", err)
	}
	st, err := fsstore.New(fsstore.Config{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := &Server{store: st}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "parent",
		Selector: "direct",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "parent",
		PartitionRequest: []byte(`{"batch_pick":{"max_items":1}}`),
	}
	_, err = srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope batch_pick against a direct-address parent with no `policy` field must be rejected, got nil error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SplitScope: code = %v, want InvalidArgument", status.Code(err))
	}
	if !strings.Contains(err.Error(), "was not opened against a pick policy") {
		t.Fatalf("SplitScope error %q must explain the missing policy", err.Error())
	}
}

func TestSplitScope_BatchPick_DirectAddressParentWithExplicitPolicySucceeds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "direct"), 0o755); err != nil {
		t.Fatalf("mkdir direct: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "queue"), 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	for _, name := range []string{"f1", "f2"} {
		if err := os.MkdirAll(filepath.Join(root, "queue", name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	pp := &fsstore.PickPolicy{
		Root:              "queue",
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := fsstore.New(fsstore.Config{
		Root:         root,
		PickPolicies: map[string]*fsstore.PickPolicy{"@queue": pp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := &Server{store: st}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "parent",
		Selector: "direct",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "parent",
		PartitionRequest: []byte(`{"batch_pick":{"max_items":1,"policy":"@queue"}}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope with an explicit policy against a direct-address parent must succeed: %v", err)
	}
	if got, want := len(resp.SubScopes), 1; got != want {
		t.Fatalf("SubScopes count = %d, want %d", got, want)
	}
}

func TestSplitScope_ExpandFolderShape(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	parentDir := filepath.Join(root, "expand-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	for _, name := range []string{"a.json", "b.json", "c.json", "d.txt"} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "expand-parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"expand_folder":{"filter":"*.json"}}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got, want := len(resp.SubScopes), 3; got != want {
		t.Fatalf("SubScopes count = %d, want %d", got, want)
	}
	gotKeys := make([]string, 0, len(resp.SubScopes))
	for _, sub := range resp.SubScopes {
		gotKeys = append(gotKeys, sub.PartitionKey)
		if filepath.Ext(sub.PartitionKey) != ".json" {
			t.Errorf("PartitionKey=%q is not *.json", sub.PartitionKey)
		}
		var addrPath string
		if err := json.Unmarshal(sub.Address, &addrPath); err != nil {
			t.Errorf("PartitionKey=%q: Address not JSON string: %v", sub.PartitionKey, err)
			continue
		}
		if filepath.Base(addrPath) != sub.PartitionKey {
			t.Errorf("PartitionKey=%q: Address basename %q does not match", sub.PartitionKey, filepath.Base(addrPath))
		}
	}
	sort.Strings(gotKeys)
	want := []string{"a.json", "b.json", "c.json"}
	for i := range want {
		if gotKeys[i] != want[i] {
			t.Errorf("PartitionKey[%d] = %q, want %q", i, gotKeys[i], want[i])
		}
	}
}

func TestSplitScope_ExpandFolderScopeMatchesDirectOpen(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	parentDir := filepath.Join(root, "expand-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "a.json"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write a.json: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId: "p1", Selector: "expand-parent", Intent: "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	resp, err := srv.SplitScope(context.Background(), &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"expand_folder":{"filter":"*.json"}}`),
	})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	var fanoutScope []byte
	for _, sub := range resp.SubScopes {
		if sub.PartitionKey == "a.json" {
			fanoutScope = sub.ClaimScopeData
		}
	}
	if fanoutScope == nil {
		t.Fatal("no fan-out sub-scope emitted for a.json")
	}
	direct, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId: "d1", Selector: "expand-parent/a.json", Intent: "rw",
	})
	if err != nil {
		t.Fatalf("Open direct: %v", err)
	}
	directScope := direct.GetAcquired().GetClaimScope()
	if !bytes.Equal(fanoutScope, directScope) {
		t.Errorf("fan-out scope %s must byte-equal a direct Open of the same file %s; otherwise the byte-equal conflict predicate cannot relate the two rw claims",
			string(fanoutScope), string(directScope))
	}
}

func TestSplitScope_UnknownDiscriminatorRejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"banana":{}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for unknown discriminator, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("SplitScope: expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("SplitScope: code = %v, want InvalidArgument", st.Code())
	}
	msg := st.Message()
	for _, want := range []string{"list", "batch_pick", "expand_folder"} {
		if !strings.Contains(msg, want) {
			t.Errorf("SplitScope: message %q should mention %q", msg, want)
		}
	}
}

func TestSplitScope_BatchPickCommitClearsInProgress(t *testing.T) {
	srv, _, root := newServerWithPickPolicyAndSync(t, "queue", action.Action{Kind: action.Pop}, "on_drain")
	for _, name := range []string{"f1", "f2", "f3", "f4"} {
		if err := os.MkdirAll(filepath.Join(root, "queue", name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "parent",
		Selector: "@queue",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	resp, err := srv.SplitScope(context.Background(), &genv1.SplitScopeRequest{
		ClaimHandleId:    "parent",
		PartitionRequest: []byte(`{"batch_pick":{"max_items":2}}`),
	})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got := len(resp.SubScopes); got != 2 {
		t.Fatalf("SubScopes count = %d, want 2", got)
	}
	subFolders := map[string]bool{}
	for _, sub := range resp.SubScopes {
		subFolders[sub.PartitionKey] = true
	}

	inProgDir := filepath.Join(root, ".fs-store", "queue", "in_progress")
	entriesBefore, err := os.ReadDir(inProgDir)
	if err != nil {
		t.Fatalf("readdir in_progress: %v", err)
	}
	if got := len(entriesBefore); got < 3 {
		t.Fatalf("in_progress entries before Commit = %d, want >= 3 (parent + 2 sub)", got)
	}

	for i, sub := range resp.SubScopes {
		rimskySubID := fmt.Sprintf("rimsky-uuid-%d", i)
		if _, err := srv.Commit(context.Background(), &genv1.CommitRequest{
			ClaimId:    rimskySubID,
			ClaimScope: sub.ClaimScopeData,
			Address:    sub.Address,
		}); err != nil {
			t.Fatalf("Commit sub[%d] with rimsky id %q: %v", i, rimskySubID, err)
		}
	}

	entriesAfter, err := os.ReadDir(inProgDir)
	if err != nil {
		t.Fatalf("readdir in_progress after commits: %v", err)
	}
	for _, e := range entriesAfter {
		f, _, _, perr := parseFromRightForTest(e.Name())
		if perr != nil {
			t.Errorf("unparseable in_progress entry %q: %v", e.Name(), perr)
			continue
		}
		if subFolders[f] {
			t.Errorf("sub-claim folder %q (rimsky-side committed) still in in_progress after Commit; entry=%q", f, e.Name())
		}
	}
}

func parseFromRightForTest(name string) (folder, claimID string, claimedNanos int64, err error) {
	idx := strings.LastIndexByte(name, '.')
	if idx < 0 {
		return "", "", 0, fmt.Errorf("missing claimed_nanos suffix")
	}
	prev := strings.LastIndexByte(name[:idx], '.')
	if prev < 0 {
		return "", "", 0, fmt.Errorf("missing claim_id suffix")
	}
	return name[:prev], name[prev+1 : idx], 0, nil
}

func TestSplitScope_MalformedPartitionRequestRejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{not valid json`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for malformed JSON, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("SplitScope: code = %v, want InvalidArgument", st.Code())
	}
}

func TestSplitScope_ExpandFolderInvalidFilterRejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	parentDir := filepath.Join(root, "expand-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "expand-parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"expand_folder":{"filter":"["}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for invalid glob filter, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("SplitScope: code = %v, want InvalidArgument", st.Code())
	}
	if !strings.Contains(st.Message(), "filter") {
		t.Errorf("message %q should mention filter", st.Message())
	}
}

func TestSplitScope_ExpandFolderUnknownKeyRejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	parentDir := filepath.Join(root, "expand-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "expand-parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"expand_folder":{"filterr":"*.json"}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for unknown expand_folder field, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("SplitScope: code = %v, want InvalidArgument", st.Code())
	}
}

func TestSplitScope_ExpandFolderFoldersDepthGT1Rejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	parentDir := filepath.Join(root, "expand-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "expand-parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"expand_folder":{"kind":"folders","depth":2}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for kind=folders+depth>1, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("SplitScope: code = %v, want InvalidArgument", st.Code())
	}
}

func TestSplitScope_ExpandFolderBothDepthGT1Rejected(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	parentDir := filepath.Join(root, "expand-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "expand-parent",
		Intent:   "rw",
	}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"expand_folder":{"kind":"both","depth":3}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for kind=both+depth>1, got nil")
	}
}

func TestSplitScope_UnknownParentClaimRejected(t *testing.T) {
	srv, _, _ := newServerWithStore(t)
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "nonexistent",
		PartitionRequest: []byte(`{"list":[{"key":"a"}]}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected NotFound for unknown claim, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("SplitScope: expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("SplitScope: code = %v, want NotFound", st.Code())
	}
}

func TestSplitScope_ClosedParentClaimRejectedAsFailedPrecondition(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.MkdirAll(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	openResp, err := srv.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "p1",
		Selector: "parent",
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	if _, err := srv.Commit(context.Background(), &genv1.CommitRequest{
		ClaimId:    "p1",
		ClaimScope: openResp.GetAcquired().GetClaimScope(),
		Address:    openResp.GetAcquired().GetAddress(),
	}); err != nil {
		t.Fatalf("Commit parent: %v", err)
	}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"list":[{"key":"a"}]}`),
	}
	_, err = srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected FailedPrecondition for a committed (non-open) parent claim, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("SplitScope: expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("SplitScope: code = %v, want FailedPrecondition (matches the postgres producer's code for the same non-open-parent condition)", st.Code())
	}
}
