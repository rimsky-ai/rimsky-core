// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package scopesconflict is the full-stack proof for
// S-claimproducer-scopesconflict-wired: a producer advertising a
// NON-TRIVIAL ScopesConflict predicate (prefix-containment) must be
// consulted by rimsky during claim acquisition so two writers whose
// scopes overlap — but are NOT byte-equal — cannot both hold the claim,
// both on the top-level acquisition path and the fan-out sub-claim path.
//
// RED CONTRACT (this pass): rimsky's `evaluateClaimScopeConflict` compares
// scopes byte-equal only and never calls the producer's ScopesConflict,
// and `AcquireSubClaims` runs no conflict check at all. So today BOTH
// overlapping (non-byte-equal) writers acquire, and BOTH overlapping
// sub-claim rows commit — the single-acquirer / single-sub-claim
// assertions below FAIL. A later pass wires the producer's ScopesConflict
// into both paths (invariant 4b) and turns this test green.
//
// The overlap producer (test/overlapproducer) runs as a CONTAINER on the
// shared docker network, reached from rimsky by a stable in-network alias
// that is up BEFORE rimsky boots — so rimsky's eager startup Capabilities
// handshake reaches it deterministically (an in-process producer behind
// the host-port tunnel races rimsky's startup dial and flakes). The
// rimsky stack is a real all-in-one container on real Postgres
// (testcontainers); run `make core-images` + `make service-images`
// first.
package scopesconflict

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// overlapProducerName is the claim-producer name both sub-cases declare
// in their templates and register the overlap producer container under.
const overlapProducerName = "overlap"

// terminalStates are node states the scheduler considers terminal for the
// success path. A held subgraph that commits leaves its members at
// "fresh"; an errored node settles "failed".
var terminalStates = map[string]bool{"fresh": true, "failed": true}

func TestScopesConflict_OverlapHeldOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// One overlap producer + one rimsky stack back BOTH sub-cases. The
	// producer and executor stub come up on the shared network BEFORE
	// rimsky: rimsky eager-dials its declared producers for a Capabilities
	// handshake at startup and exits non-zero if any is unreachable.
	netName := harness.NewNetwork(ctx, t)
	producerEndpoint := harness.StartOverlapClaimProducerOnNetwork(ctx, t, netName, "overlap-producer")
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer(overlapProducerName, producerEndpoint, "sync"),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(%s): %v", ep.HostDSN, err)
	}
	t.Cleanup(pool.Close)

	t.Run("top_level_acquisition", func(t *testing.T) {
		runTopLevelOverlapCase(ctx, t, ep, pool)
	})
	t.Run("fanout_subclaim", func(t *testing.T) {
		runFanOutOverlapCase(ctx, t, ep, pool)
	})
}

// runTopLevelOverlapCase drives an instance where an acquirer durably
// holds a claim on the parent prefix `tenant/a` (held subgraph → Commit →
// committed+durable row lingering in the conflict set) and a contender
// then acquires the CHILD scope `tenant/a/x`. The two scopes overlap by
// the producer's prefix predicate but are NOT byte-equal.
//
// Assertion: exactly ONE acquired claim_scope row for the overlap
// producer exists on this instance — the contender was held off because
// rimsky consulted the producer's ScopesConflict. Today (RED) the
// contender ALSO acquires (byte-equal-only check misses the overlap), so
// the count is 2 and this assertion fails.
func runTopLevelOverlapCase(ctx context.Context, t *testing.T, ep harness.RimskyEndpoint, pool *pgxpool.Pool) {
	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "scopes-conflict-top-level",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				// acquirer: durably holds the PARENT-prefix scope `tenant/a`.
				{
					"type":     "acquirer",
					"executor": "stub",
					"stores": []map[string]any{
						{
							"name":     overlapProducerName,
							"selector": "tenant/a",
							"intent":   "rw",
							"alias":    "held",
							"lifetime": "durable",
						},
					},
				},
				// verifier: co-holds `held`, succeeds → auto-terminal Commit
				// promotes the acquirer's durable row to committed+durable,
				// which lingers in the conflict set.
				{
					"type":     "verifier",
					"executor": "stub",
					"holds": map[string]any{
						"held": map[string]any{"from": "acquirer"},
					},
					"subscribes": []map[string]any{
						{"node": "acquirer", "type": "terminal/*"},
					},
				},
				// contender: acquires the CHILD scope `tenant/a/x` AFTER the
				// held subgraph commits (subscribes to the verifier's
				// terminal). `tenant/a` ⊏ `tenant/a/x` overlap by the
				// producer's prefix predicate, NOT byte-equal.
				{
					"type":     "contender",
					"executor": "stub",
					"stores": []map[string]any{
						{
							"name":     overlapProducerName,
							"selector": "tenant/a/x",
							"intent":   "rw",
						},
					},
					"subscribes": []map[string]any{
						{"node": "verifier", "type": "terminal/*"},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-scopes-conflict-top-level")

	// The held subgraph must settle first (acquirer + verifier fresh) so
	// the durable claim row is committed and occupying `tenant/a` in the
	// conflict set before the contender attempts.
	waitForNodeTerminal(t, ep, instanceID, "acquirer", 120*time.Second)
	waitForNodeTerminal(t, ep, instanceID, "verifier", 120*time.Second)

	// Give the contender time to be scheduled and ATTEMPT its overlapping
	// acquire, then assert only ONE writer ever holds the overlapping
	// scope. Today (RED) the contender's `tenant/a/x` is not byte-equal to
	// the durable `tenant/a`, so it acquires its OWN claim row alongside —
	// TWO acquired rows. The wired behavior (later pass) consults the
	// producer's ScopesConflict (`tenant/a` ⊏ `tenant/a/x`), bails the
	// contender BEFORE its row is INSERTed, and leaves only the durable
	// acquirer's row.
	//
	// Poll for the two-writer violation across a window (the contender's
	// own committed claim is subgraph-lifetime and could be reaped, so a
	// single late read could miss it): the moment two acquired rows
	// coexist is the violation. If the window elapses without ever seeing
	// two, the steady state is the single durable holder — assert exactly
	// one.
	deadline := time.Now().Add(30 * time.Second)
	got := 1
	for time.Now().Before(deadline) {
		got = countAcquiredClaimScopeRows(ctx, t, pool, instanceID)
		if got >= 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got != 1 {
		t.Fatalf("top-level overlap: want exactly 1 acquired claim_scope row for producer %q "+
			"(only the durable acquirer; the contender's overlapping non-byte-equal scope held off), "+
			"got %d — rimsky did not consult the producer's ScopesConflict during acquisition",
			overlapProducerName, got)
	}
}

// runFanOutOverlapCase drives an instance whose fan-out parent splits into
// two OVERLAPPING sub-scopes (`tenant/a` and `tenant/a/x`, prefix-
// overlapping, NOT byte-equal). The acquisition tx must NOT commit both
// overlapping sub-claim rows — the conflicting one is rejected.
//
// Assertion: at most ONE sub-claim row (parent_claim_handle_id NOT NULL)
// for the overlap producer exists on this instance. Today (RED)
// AcquireSubClaims runs no conflict check, so BOTH overlapping sub-claim
// rows commit (count 2) and this assertion fails.
func runFanOutOverlapCase(ctx context.Context, t *testing.T, ep harness.RimskyEndpoint, pool *pgxpool.Pool) {
	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "scopes-conflict-fanout",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "fan-parent",
					"executor": "stub",
					"stores": []map[string]any{
						{
							"name":     overlapProducerName,
							"selector": "tenant",
							"intent":   "rw",
							"alias":    "data",
						},
					},
					// SplitScope keys `a` and `a/x` → sub-scopes `tenant/a`
					// and `tenant/a/x`, which overlap by the prefix predicate.
					"fan_out": map[string]any{
						"claim":             "data",
						"partition_request": `{"partition_keys":["a","a/x"]}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-scopes-conflict-fanout")

	// Drive the fan-out parent's acquisition. The observable is the
	// COMMITTED sub-claim rows the acquisition tx leaves. Today (RED)
	// AcquireSubClaims conflict-checks NOTHING, so it INSERTs BOTH
	// overlapping sub-scopes and the tx commits both — observable promptly.
	// The wired behavior (later pass) aborts the acquisition tx when the
	// second overlapping sub-scope conflicts, so NEITHER sibling sub-claim
	// row commits (invariant 10 atomicity) — the conflicting wave never
	// lands.
	//
	// The assertion is the spec's literal contract: the acquisition tx must
	// NOT commit BOTH overlapping sub-claim rows. So we fail when both are
	// present. We poll until the fan-out has been driven (either committed
	// sub-claims appear — the RED path — or a bounded settle window elapses
	// during which the wired path would have aborted), then assert fewer
	// than both committed. In RED the two rows appear within a few ticks
	// and fail this gate; in the wired path the window elapses with zero
	// rows and the gate passes.
	deadline := time.Now().Add(45 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		got = countSubClaimRows(ctx, t, pool, instanceID)
		if got >= 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got >= 2 {
		t.Fatalf("fan-out overlap: the acquisition tx committed BOTH overlapping sub-claim rows for "+
			"producer %q (got %d), but the conflicting sub-claim must be rejected so the tx commits "+
			"neither — AcquireSubClaims did not conflict-check the overlapping sub-scopes against the "+
			"producer's ScopesConflict", overlapProducerName, got)
	}
}

// countAcquiredClaimScopeRows counts the acquired top-level claim_scope
// rows (NOT sub-claims) for the overlap producer on the given instance —
// rows that have a bound address (Open succeeded) and no parent (not a
// fan-out sub-claim). claim_handles link to an instance via
// holder_node_id → rimsky_nodes.instance_id.
func countAcquiredClaimScopeRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.lock_kind = 'claim_scope'
		    AND ch.producer_name = $2
		    AND ch.parent_claim_handle_id IS NULL
		    AND ch.address IS NOT NULL`,
		instanceID, overlapProducerName,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count acquired claim_scope rows: %v", err)
	}
	return n
}

// countSubClaimRows counts the fan-out sub-claim rows (parent_claim_handle_id
// NOT NULL) for the overlap producer on the given instance.
func countSubClaimRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.lock_kind = 'claim_scope'
		    AND ch.producer_name = $2
		    AND ch.parent_claim_handle_id IS NOT NULL`,
		instanceID, overlapProducerName,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count sub-claim rows: %v", err)
	}
	return n
}

// deployTemplate POSTs body to /templates then deploys it; returns the
// template id.
func deployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
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

// createInstance POSTs a new instance and returns its instance_id.
func createInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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
	return resp.InstanceID
}

// waitForNodeTerminal polls the node-state observability route until the
// node reaches a terminal state; fails hard on deadline.
func waitForNodeTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
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
				if terminalStates[lastState] {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach terminal within %v; last state=%q",
		nodeType, instanceID, deadline, lastState)
}
