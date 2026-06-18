// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package atomicstaging

import (
	"context"
	"encoding/json"
	"net/http"
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

	netName := harness.NewNetwork(ctx, t)

	popAndMoveCommitted := "{pop_and_move: " + fsCommittedSubdir + "}"
	store := harness.StartFilesystemStore(ctx, t, netName, "store-fs", harness.FilesystemStoreSpec{
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

	okEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")
	errEndpoint := harness.StartErroringExecutorStubOnNetwork(ctx, t, netName, "exec-err")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("content", store.InternalEndpoint, "sync"),
		harness.WithExecutor("ok", okEndpoint),
		harness.WithExecutor("err", errEndpoint),
	)

	commitTemplateID := deployHeldSwapTemplate(t, ep, "fs-held-swap-commit", fsCommitSelector, "ok")
	commitInstanceID := createHeldSwapInstance(t, ep, commitTemplateID, "ck-fs-held-swap-commit")

	waitForNodeState(t, ep, commitInstanceID, "acquirer", "fresh", 120*time.Second)
	waitForNodeState(t, ep, commitInstanceID, "verifier", "fresh", 120*time.Second)

	committedDst := filepath.Join(store.HostDir, fsCommittedSubdir, fsCommitFolder)
	sourceSrc := filepath.Join(store.HostDir, fsCommitSource, fsCommitFolder)
	requireEventuallyMoved(t, committedDst, sourceSrc, 30*time.Second)

	abandonTemplateID := deployHeldSwapTemplate(t, ep, "fs-held-swap-abandon", fsAbandonSelector, "err")
	abandonInstanceID := createHeldSwapInstance(t, ep, abandonTemplateID, "ck-fs-held-swap-abandon")

	waitForNodeState(t, ep, abandonInstanceID, "acquirer", "fresh", 120*time.Second)
	waitForNodeState(t, ep, abandonInstanceID, "verifier", "failed", 120*time.Second)

	abandonCommittedDst := filepath.Join(store.HostDir, fsCommittedSubdir, fsAbandonFolder)
	abandonSource := filepath.Join(store.HostDir, fsAbandonSource, fsAbandonFolder)
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
			{"node": "acquirer", "type": "terminal/*", "wake_on_change": true, "force_upstream_refresh": false},
		},
	}
	if verifierExecutor == "err" {
		verifierNode["error_types"] = map[string]any{
			"stub/forced_error": map[string]any{
				"policy": []map[string]any{
					{"action": "give_up"},
				},
			},
		}
	}

	body := map[string]any{
		"spec": map[string]any{
			"name":             name,
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "acquirer",
					"executor": "ok",
					"stores": []map[string]any{
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
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates (%s): %d %s", name, status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createHeldSwapInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "held-swap", instanceKey)
	return resp.InstanceID
}

func waitForNodeState(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType, want string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State string `json:"state"`
				} `json:"node"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				if lastState == want {
					return
				}
				if isTerminalState(lastState) && lastState != want {
					t.Fatalf("node %q on instance %s settled in %q, want %q",
						nodeType, instanceID, lastState, want)
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach %q within %v; last state=%q",
		nodeType, instanceID, want, deadline, lastState)
}

func isTerminalState(state string) bool {
	return state == "failed"
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
