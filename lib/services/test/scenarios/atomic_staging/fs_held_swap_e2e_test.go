// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package atomicstaging

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const (
	fsCommitSelector  = "@content-commit"
	fsCommitSource    = "incoming-commit"
	fsCommitFolder    = "batch-commit"
	fsCommittedSubdir = "committed"

	fsAbandonSelector = "@content-abandon"
	fsAbandonSource   = "incoming-abandon"
	fsAbandonFolder   = "batch-abandon"
)

func TestFilesystemStageThenSwap_HeldSubgraphE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)

	popAndMoveCommitted := "{pop_and_move: " + fsCommittedSubdir + "}"
	producer := harness.StartFilesystemClaimProducer(ctx, t, netName, "producer-fs", harness.FilesystemClaimProducerSpec{
		PickPolicies: map[string]harness.FilesystemPickPolicy{
			fsCommitSelector: {
				Root:                     fsCommitSource,
				OnCommit:                 popAndMoveCommitted,
				OnGiveUp:                 "recycle",
				VisibilityTimeoutSeconds: 1800,
				SyncStrategy:             "on_open",
			},
			fsAbandonSelector: {
				Root:                     fsAbandonSource,
				OnCommit:                 popAndMoveCommitted,
				OnGiveUp:                 "recycle",
				VisibilityTimeoutSeconds: 1800,
				SyncStrategy:             "on_open",
			},
		},
		SeedFolders: [][]string{
			{fsCommitSource, fsCommitFolder},
			{fsAbandonSource, fsAbandonFolder},
			{fsCommittedSubdir},
		},
	})

	okEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)
	errEndpoint := harness.StartErroringExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("content", producer.InternalEndpoint, "sync"),
		harness.WithExecutor("ok", okEndpoint),
		harness.WithExecutor("err", errEndpoint),
	)

	commitTemplateID := deployHeldSwapTemplate(t, ep, "fs-held-swap-commit", fsCommitSelector, "ok")
	commitInstanceID := ep.CreateInstance(t, commitTemplateID, "ck-fs-held-swap-commit", "held-swap")

	ep.WaitForNodeSettledTo(t, commitInstanceID, "acquirer", "fresh")
	ep.WaitForNodeSettledTo(t, commitInstanceID, "verifier", "fresh")

	committedDst := filepath.Join(producer.HostDir, fsCommittedSubdir, fsCommitFolder)
	sourceSrc := filepath.Join(producer.HostDir, fsCommitSource, fsCommitFolder)
	requireEventuallyMoved(t, committedDst, sourceSrc, 30*time.Second)

	abandonTemplateID := deployHeldSwapTemplate(t, ep, "fs-held-swap-abandon", fsAbandonSelector, "err")
	abandonInstanceID := ep.CreateInstance(t, abandonTemplateID, "ck-fs-held-swap-abandon", "held-swap")

	ep.WaitForNodeSettledTo(t, abandonInstanceID, "verifier", "failed")
	ep.WaitForNodeSettledTo(t, abandonInstanceID, "acquirer", "failed")

	abandonCommittedDst := filepath.Join(producer.HostDir, fsCommittedSubdir, fsAbandonFolder)
	abandonSource := filepath.Join(producer.HostDir, fsAbandonSource, fsAbandonFolder)
	requireNotMovedIntoCommitted(t, abandonCommittedDst, abandonSource, 20*time.Second)
}

func deployHeldSwapTemplate(t *testing.T, ep harness.RimskyEndpoint, name, selector, verifierExecutor string) string {
	t.Helper()
	verifierNode := map[string]any{
		"type":     "verifier",
		"executor": verifierExecutor,
		"holds": map[string]any{
			"held": map[string]any{"from": "acquirer"},
		},
		"subscribes": []map[string]any{
			{"node": "acquirer", "type": "terminal/*", "force_upstream_refresh": false},
		},
	}
	if verifierExecutor == "err" {
		verifierNode["error_types"] = map[string]any{
			"stub/forced_error": map[string]any{
				"action": "give_up",
			},
		}
	}

	body := map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "acquirer",
					"executor": "ok",
					"claim_producers": []map[string]any{
						{
							"name":     "content",
							"selector": selector,
							"intent":   "rw",
							"alias":    "held",
						},
					},
				},
				verifierNode,
			},
		},
	}
	return ep.DeployTemplate(t, body)
}

func requireEventuallyMoved(t *testing.T, dst, src string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		_, dstErr := os.Stat(dst)
		_, srcErr := os.Stat(src)
		if dstErr == nil && os.IsNotExist(srcErr) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	dstExists := pathExists(dst)
	srcExists := pathExists(src)
	t.Fatalf("staged folder was not swapped into the committed dir within %v: "+
		"committed=%q exists=%v, source=%q exists=%v — the real pop_and_move "+
		"os.Rename did not run through the held-subgraph auto-terminal Commit",
		deadline, dst, dstExists, src, srcExists)
}

func requireNotMovedIntoCommitted(t *testing.T, committedDst, src string, settle time.Duration) {
	t.Helper()
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		if pathExists(committedDst) {
			t.Fatalf("staged folder was moved into the committed dir %q on the abandon path — "+
				"aggregate-failure must NOT swap staging into production", committedDst)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if pathExists(committedDst) {
		t.Fatalf("staged folder was moved into the committed dir %q on the abandon path", committedDst)
	}
	if !pathExists(src) {
		t.Fatalf("staged folder %q is gone from the source dir after Abandon; "+
			"on_give_up=recycle must leave the folder in place (the abandon drops "+
			"the staging-commit, it does not delete the staged data)", src)
	}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || !os.IsNotExist(err)
}
