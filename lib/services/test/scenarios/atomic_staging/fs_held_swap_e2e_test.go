// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Gate 10 — a real filesystem producer's stage-then-swap is the
// value-delivering component in a held subgraph driven end-to-end through
// a real rimsky stack.
//
// `concept:atomic-staging`'s headline claim is that held subgraphs
// commit-or-abandon atomically: on aggregate success the producer
// atomically swaps staged data into the canonical view; on any failure it
// drops the staging. Every prior test of that claim either stubs the
// producer (the scenario-harness held-subgraph tests use the loopback stub
// store, whose Commit/Abandon are call-counters, never a real swap), or
// drives the real producer's swap over a direct gRPC client with no rimsky
// stack, no `holds:`, and no cascade (the fs pick-policy wire exerciser and
// the postgres fused-store scenario). The postgres store the held-subgraph
// e2e historically pointed at has a Commit that is a deliberate no-op for
// scope-bytes claims — so the "value path" was never the value path.
//
// This test puts a REAL filesystem producer's `os.Rename` swap and rimsky's
// real held-subgraph auto-terminal dispatch together:
//
//   - Commit case: an acquirer (executor: ok) opens a pick-policy claim on
//     a real rimsky-store-filesystem image, staging a folder; a co-holding
//     verifier (executor: ok) co-holds it via `holds:` and subscribes to
//     the acquirer's terminal. Both succeed → auto-terminal fires one
//     aggregate Commit → the producer's real pop_and_move renames the
//     staged folder into the `committed` target dir. The test asserts the
//     on-disk move via the store's host bind-mount.
//   - Abandon case: same shape, but the verifier uses executor: err (the
//     erroring stub, error_class=stub/forced_error, routed to give_up so
//     the failure is terminal). Aggregate failure → auto-terminal fires
//     Abandon → on_give_up=recycle leaves the staged folder in place; the
//     test asserts it was NOT moved into `committed`.
//
// The two cases use two stub-executor instances because the stub cannot
// vary its outcome per node within one instance — one success stub
// (exec-ok), one error stub (exec-err). The acquirer always uses exec-ok
// (it must acquire + stage); only the verifier's executor differs.
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
	// Two independent pick policies, one per case, each with its own
	// source dir seeded with EXACTLY ONE folder. Separate policies remove
	// the FIFO ambiguity of a shared queue (the pick policy hands out the
	// oldest-then-alphabetically-first available folder per Open; a shared
	// source dir would make which folder a given instance stages
	// nondeterministic). The store strips the leading "@" for its on-disk
	// state dir; the selector is matched verbatim against the configured
	// pick_policies map key.

	// Commit case: source `incoming-commit/`, swap target `committed/`.
	fsCommitSelector  = "@content-commit"
	fsCommitSource    = "incoming-commit"
	fsCommitFolder    = "batch-commit"
	fsCommittedSubdir = "committed"

	// Abandon case: source `incoming-abandon/`, swap target `committed/`
	// (shared target dir; the abandon path must NOT move into it).
	fsAbandonSelector = "@content-abandon"
	fsAbandonSource   = "incoming-abandon"
	fsAbandonFolder   = "batch-abandon"
)

// TestFilesystemStageThenSwap_HeldSubgraphE2E drives the real filesystem
// store's stage-then-swap through rimsky's held-subgraph auto-terminal:
// aggregate-success → a real os.Rename into the committed dir;
// aggregate-failure → the staging is left in place (no swap into
// production).
func TestFilesystemStageThenSwap_HeldSubgraphE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Bring up the shared network first so the fs-store and both executor
	// stubs are reachable when rimsky fires its startup claim-producer /
	// executor Capabilities handshake.
	netName := harness.NewNetwork(ctx, t)

	// Real filesystem store. Both pick policies use OnCommit =
	// pop_and_move(committed) — the real swap — and OnGiveUp = recycle (the
	// abandon path leaves the folder in its source dir, NOT moved into
	// committed). Each policy's source dir is seeded with exactly one
	// folder, plus the shared committed/ target dir which MUST exist at
	// store-config load time (validateMoveTargetSameFS stats it).
	//
	// The one-key flow-map `{pop_and_move: committed}` is required because
	// a bare `pop_and_move` string is rejected by action.UnmarshalYAML
	// (pop_and_move requires an inline target); the harness renders
	// `on_commit: %s` verbatim, so the value must itself be valid YAML.
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

	// Two executor stubs: a success stub (exec-ok) and an error stub
	// (exec-err). The acquirer always uses ok; the verifier's executor
	// selects the aggregate outcome.
	okEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")
	errEndpoint := harness.StartErroringExecutorStubOnNetwork(ctx, t, netName, "exec-err")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("content", store.InternalEndpoint, "sync"),
		harness.WithExecutor("ok", okEndpoint),
		harness.WithExecutor("err", errEndpoint),
	)

	// --- Commit case ---------------------------------------------------
	commitTemplateID := deployHeldSwapTemplate(t, ep, "fs-held-swap-commit", fsCommitSelector, "ok")
	commitInstanceID := createHeldSwapInstance(t, ep, commitTemplateID, "ck-fs-held-swap-commit")

	// Both nodes must settle fresh (acquirer success → verifier success →
	// aggregate Commit). The verifier reaching fresh is the cue the held
	// subgraph completed and auto-terminal fired.
	waitForNodeState(t, ep, commitInstanceID, "acquirer", "fresh", 120*time.Second)
	waitForNodeState(t, ep, commitInstanceID, "verifier", "fresh", 120*time.Second)

	// The real pop_and_move must have moved the staged folder out of the
	// source dir and into committed/. Poll the on-disk state via the
	// host bind-mount — the producer's Commit runs asynchronously after
	// the verifier settles.
	committedDst := filepath.Join(store.HostDir, fsCommittedSubdir, fsCommitFolder)
	sourceSrc := filepath.Join(store.HostDir, fsCommitSource, fsCommitFolder)
	requireEventuallyMoved(t, committedDst, sourceSrc, 30*time.Second)

	// --- Abandon case --------------------------------------------------
	abandonTemplateID := deployHeldSwapTemplate(t, ep, "fs-held-swap-abandon", fsAbandonSelector, "err")
	abandonInstanceID := createHeldSwapInstance(t, ep, abandonTemplateID, "ck-fs-held-swap-abandon")

	// The acquirer succeeds (executor: ok), the verifier fails (executor:
	// err → stub/forced_error → give_up). Wait for the verifier to settle
	// failed; auto-terminal then aggregates failure → Abandon.
	waitForNodeState(t, ep, abandonInstanceID, "acquirer", "fresh", 120*time.Second)
	waitForNodeState(t, ep, abandonInstanceID, "verifier", "failed", 120*time.Second)

	// The abandon path (OnGiveUp=recycle) must NOT move the staged folder
	// into committed/. Give the producer's Abandon time to run, then
	// assert the committed/ target never received batch-abandon and the
	// folder is still on disk in the source dir.
	abandonCommittedDst := filepath.Join(store.HostDir, fsCommittedSubdir, fsAbandonFolder)
	abandonSource := filepath.Join(store.HostDir, fsAbandonSource, fsAbandonFolder)
	requireNotMovedIntoCommitted(t, abandonCommittedDst, abandonSource, 20*time.Second)
}

// deployHeldSwapTemplate POSTs a held-subgraph template (acquirer +
// co-holding verifier) to /templates and deploys it. verifierExecutor
// selects the verifier's executor ("ok" → success → Commit; "err" →
// failure → Abandon). The acquirer always uses "ok" so it acquires and
// stages. Returns the template id.
//
// `holds:` is expressed as JSON (not a graph/node struct) because the
// consumption-side-isolation depguard bars lib/services from importing
// lib/graph/node. The shape mirrors the in-process held-subgraph test:
// an acquirer with a claim aliased `held` (lifetime defaults to subgraph)
// on the `content` producer at the pick-policy selector, and a co-holding
// verifier that declares `holds: {held: {from: acquirer}}` and subscribes
// to the acquirer's `terminal/*`.
func deployHeldSwapTemplate(t *testing.T, ep harness.RimskyEndpoint, name, selector, verifierExecutor string) string {
	t.Helper()
	verifierNode := map[string]any{
		"type":     "verifier",
		"executor": verifierExecutor,
		"holds": map[string]any{
			"held": map[string]any{"from": "acquirer"},
		},
		"subscribes": []map[string]any{
			{"node": "acquirer", "type": "terminal/*"},
		},
	}
	if verifierExecutor == "err" {
		// Route the erroring stub's class to give_up so the verifier's
		// failure is terminal (not a retry loop). Without this an erroring
		// node with no policy would still settle failed by default, but
		// the explicit give_up makes the terminal deterministic and
		// matches the give_up_test.go shape (error class keyed by the
		// stub's hierarchical class).
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
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
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
	status, raw := ep.PostJSON(t, "/templates", body)
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
	deployStatus, deployRaw := ep.PostJSON(t, "/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createHeldSwapInstance POSTs a new instance and returns its instance_id.
func createHeldSwapInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/instances", map[string]any{
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
	return resp.InstanceID
}

// waitForNodeState polls the node-state observability route until the
// node reaches want (or a deadline). Fails hard on timeout so an absent
// transition is a real failure, never a skip.
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
				// A node that settled failed when we wanted fresh (or vice
				// versa) will never recover — stop early with a clear message.
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

// isTerminalState reports whether a node state is a settled terminal the
// wait loop should not keep polling past (when it differs from the wanted
// state). `running`/`stale`/`fresh` are transient-or-target; `failed` is
// terminal. `fresh` is also terminal but is the success target, so the
// caller's want-match short-circuits before this is consulted for it.
func isTerminalState(state string) bool {
	return state == "failed"
}

// requireEventuallyMoved asserts that within deadline the staged folder
// appears at dst (the committed target) AND is gone from src (the source
// dir) — proving the real pop_and_move os.Rename ran. Polls because the
// producer's Commit fires asynchronously after the verifier settles.
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

// requireNotMovedIntoCommitted asserts that the staged folder was NOT
// moved into the committed dir (the abandon path ran; no swap into
// production), and is still present in the source dir. It waits a fixed
// settle window first so a late, erroneous swap would still be caught.
func requireNotMovedIntoCommitted(t *testing.T, committedDst, src string, settle time.Duration) {
	t.Helper()
	// Wait out the settle window so any (erroneous) Commit-side swap has
	// time to land before we assert it did not.
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

// pathExists reports whether the path exists (any stat error other than
// not-exist is treated as "exists" so a permission glitch doesn't mask a
// real move).
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || !os.IsNotExist(err)
}
