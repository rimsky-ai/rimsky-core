// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestAtomicStaging_CommitOnAllSuccess(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client, stop := startProducer(t, st)
	defer stop()
	ctx := context.Background()

	const scope = "dataset-alpha"
	staging := openStaging(ctx, t, client, "commit-all-1", scope)

	writers := []struct{ name, body string }{
		{"part-1.json", `{"rows":1}`},
		{"part-2.json", `{"rows":2}`},
		{"part-3.json", `{"rows":3}`},
	}
	for _, w := range writers {
		if err := os.WriteFile(filepath.Join(staging, w.name), []byte(w.body), 0o644); err != nil {
			t.Fatalf("sub-stage write %s: %v", w.name, err)
		}
	}

	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId: "commit-all-1", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("commit after all sub-stages succeeded: %v", err)
	}

	canonical := filepath.Join(root, "canonical", scope)
	for _, w := range writers {
		got, err := os.ReadFile(filepath.Join(canonical, w.name))
		if err != nil {
			t.Fatalf("canonical must hold every staged file after aggregate success: %v", err)
		}
		if string(got) != w.body {
			t.Fatalf("canonical %s = %q, want %q", w.name, got, w.body)
		}
	}
	if dirExists(staging) {
		t.Fatalf("staging %q must be swapped away, not copied", staging)
	}

	staging2 := openStaging(ctx, t, client, "commit-all-2", scope)
	if err := os.WriteFile(filepath.Join(staging2, "part-1.json"), []byte(`{"rows":10}`), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId: "commit-all-2", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("commit over pre-existing canonical: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(canonical, "part-1.json"))
	if err != nil || string(got) != `{"rows":10}` {
		t.Fatalf("second commit must atomically replace the canonical view: got %q err=%v", got, err)
	}
	if pathExists(filepath.Join(canonical, "part-2.json")) {
		t.Fatal("the swap must replace the canonical view wholesale, not merge into it")
	}
}

func TestAtomicStaging_AbandonOnAnyFailure(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client, stop := startProducer(t, st)
	defer stop()
	ctx := context.Background()

	const scope = "dataset-beta"
	seedCanonical(ctx, t, client, root, scope, "good.json", `{"v":1}`)
	canonical := filepath.Join(root, "canonical", scope)

	staging := openStaging(ctx, t, client, "abandon-any-1", scope)
	if err := os.WriteFile(filepath.Join(staging, "good.json"), []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatalf("first sub-stage write: %v", err)
	}
	failingMember := func() error { return errors.New("member failed mid-stage") }
	memberErr := failingMember()
	if memberErr == nil {
		t.Fatal("scenario requires a failing member")
	}

	if _, err := client.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId: "abandon-any-1", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("abandon after member failure: %v", err)
	}

	if dirExists(staging) {
		t.Fatalf("staging %q must be dropped on any member failure", staging)
	}
	got, err := os.ReadFile(filepath.Join(canonical, "good.json"))
	if err != nil || string(got) != `{"v":1}` {
		t.Fatalf("canonical must be untouched after abandon-on-any-failure: got %q err=%v", got, err)
	}
}

func TestAtomicStaging_ConcurrentStaging(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client, stop := startProducer(t, st)
	defer stop()
	ctx := context.Background()

	const scope = "dataset-shared"
	winner := openStaging(ctx, t, client, "concurrent-winner", scope)
	loser := openStaging(ctx, t, client, "concurrent-loser", scope)
	if winner == loser {
		t.Fatalf("each Open must reserve a private staging area keyed by (scope, claim_id); both claims got %q", winner)
	}
	otherScope := openStaging(ctx, t, client, "concurrent-other", "dataset-other")

	var wg sync.WaitGroup
	stagingByDir := map[string]string{
		winner:     "winner-bytes",
		loser:      "loser-bytes",
		otherScope: "other-bytes",
	}
	for dir, body := range stagingByDir {
		wg.Add(1)
		go func(dir, body string) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				name := fmt.Sprintf("chunk-%d.txt", i)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Errorf("concurrent stage write %s: %v", filepath.Join(dir, name), err)
					return
				}
			}
		}(dir, body)
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId: "concurrent-winner", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("commit winner: %v", err)
	}
	if _, err := client.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId: "concurrent-loser", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("abandon loser: %v", err)
	}
	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId: "concurrent-other", ClaimScope: []byte("dataset-other"),
	}); err != nil {
		t.Fatalf("commit other scope: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "canonical", scope, "chunk-0.txt"))
	if err != nil || string(got) != "winner-bytes" {
		t.Fatalf("canonical %s must hold the committed claim's bytes, isolated from the abandoned sibling: got %q err=%v", scope, got, err)
	}
	gotOther, err := os.ReadFile(filepath.Join(root, "canonical", "dataset-other", "chunk-0.txt"))
	if err != nil || string(gotOther) != "other-bytes" {
		t.Fatalf("independent scopes must stage and commit in isolation: got %q err=%v", gotOther, err)
	}
	if dirExists(winner) || dirExists(loser) || dirExists(otherScope) {
		t.Fatal("every staging dir must be gone after its claim resolves")
	}
}

func TestAtomicStaging_SubStageVerifierFailure(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client, stop := startProducer(t, st)
	defer stop()
	ctx := context.Background()

	const scope = "dataset-verified"
	seedCanonical(ctx, t, client, root, scope, "record.json", `{"shape":"valid"}`)
	canonical := filepath.Join(root, "canonical", scope)

	staging := openStaging(ctx, t, client, "verify-1", scope)
	if err := os.WriteFile(filepath.Join(staging, "record.json"), []byte(`not-json`), 0o644); err != nil {
		t.Fatalf("stage malformed record: %v", err)
	}

	if verifyErr := verifyStagedShape(staging); verifyErr == nil {
		t.Fatal("the staged content is malformed; the verifier sub-stage must fail")
	}
	if _, err := client.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId: "verify-1", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("abandon after verifier failure: %v", err)
	}
	if dirExists(staging) {
		t.Fatalf("staging %q must be dropped when a verifier sub-stage fails", staging)
	}
	got, err := os.ReadFile(filepath.Join(canonical, "record.json"))
	if err != nil || string(got) != `{"shape":"valid"}` {
		t.Fatalf("canonical must be untouched after a verifier failure: got %q err=%v", got, err)
	}

	staging2 := openStaging(ctx, t, client, "verify-2", scope)
	if err := os.WriteFile(filepath.Join(staging2, "record.json"), []byte(`{"shape":"also-valid"}`), 0o644); err != nil {
		t.Fatalf("stage valid record: %v", err)
	}
	if verifyErr := verifyStagedShape(staging2); verifyErr != nil {
		t.Fatalf("valid staged content must pass the verifier: %v", verifyErr)
	}
	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId: "verify-2", ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("commit after verifier success: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(canonical, "record.json"))
	if err != nil || string(got) != `{"shape":"also-valid"}` {
		t.Fatalf("canonical must hold the verified staged content after commit: got %q err=%v", got, err)
	}
}

func verifyStagedShape(stagingDir string) error {
	body, err := os.ReadFile(filepath.Join(stagingDir, "record.json"))
	if err != nil {
		return fmt.Errorf("verifier: read staged record: %w", err)
	}
	if len(body) == 0 || body[0] != '{' {
		return fmt.Errorf("verifier: staged record is not a JSON object")
	}
	return nil
}

func openStaging(ctx context.Context, t *testing.T, client genv1.ClaimProducerClient, claimID, scope string) string {
	t.Helper()
	resp, err := client.Open(ctx, &genv1.OpenRequest{
		ClaimId:  claimID,
		Selector: scope,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("open %s: %v", claimID, err)
	}
	acq := resp.GetAcquired()
	if acq == nil {
		t.Fatalf("open %s did not return Acquired: %+v", claimID, resp.GetResult())
	}
	staging := string(acq.GetAddress())
	if !dirExists(staging) {
		t.Fatalf("open %s did not reserve staging %q", claimID, staging)
	}
	return staging
}

func seedCanonical(ctx context.Context, t *testing.T, client genv1.ClaimProducerClient, root, scope, name, body string) {
	t.Helper()
	staging := openStaging(ctx, t, client, "seed-"+scope, scope)
	if err := os.WriteFile(filepath.Join(staging, name), []byte(body), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId: "seed-" + scope, ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if !pathExists(filepath.Join(root, "canonical", scope, name)) {
		t.Fatalf("seed canonical missing")
	}
}
