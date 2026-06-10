// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-claim-producer-protocol: a service author's
// example ClaimProducer — registered with rimsky's catalog, advertising
// its write-semantics envelope via Capabilities, handling Open / Commit /
// Abandon / Release through the public protocol surface — plugs into a
// running rimsky stack end-to-end. Each leg of the spec's Acceptance is
// exhibited against the REAL assembled product (rimsky-all-in-one in a
// testcontainer, Postgres state DB) plus the REAL example producer
// binary (built from this directory's source via Dockerfile.example and
// brought up on the shared docker network):
//
//  1. Real Open + real Commit on success. A template referencing the
//     containerised example producer (intent: r, the producer's only
//     honest intent — it advertises read_only) on an executor stub that
//     succeeds drives a dispatch through the real supervisor; rimsky's
//     terminal pipeline fires Commit on auto-terminal. The
//     `claim_resolution.commit` event lands on /v1/events with
//     producer_name=example — proof "rimsky drives Commit at
//     auto-terminal" against the REAL producer. The
//     `claim_resolution.*` event is emitted by the per-terminal
//     forensics code path AFTER the producer's Commit RPC returns
//     successfully (lib/runtime/terminal_decision_forensics.go), so its
//     presence proves the verb really landed on the producer (not a
//     stub).
//
//  2. Real Open + real Abandon on failure. A SECOND template referencing
//     the same producer on an erroring executor stub; the node settles
//     failed; rimsky's terminal pipeline fires Abandon on auto-terminal.
//     The `claim_resolution.abandon` event lands on /v1/events with
//     producer_name=example — proof "on failure, Abandon" against the
//     REAL producer.
//
//  3. Real Release. The Release verb is invoked by rimsky on instance
//     termination for held durable claims (asset pattern; requires the
//     data_processing protocol the example producer does NOT advertise).
//     Exhibited here by driving the producer's RPC handler directly
//     through the SAME wire shape rimsky uses on a held-durable
//     terminate (lib/runtime/peer.Client.Release call site) against a
//     SECOND in-process producer on a host port; the producer's
//     releaseCalls counter and recorded claim_id prove the verb really
//     lands on the producer's handler (falsifier: "Release is called
//     but the producer's effect is canned" fails when the counter
//     doesn't grow or the claim_id is dropped).
//
//  4. Un-advertised write-semantics is refused at registration. The
//     example producer advertises ONLY read_only on Capabilities.
//     Calling the EXACT production code rimsky uses at startup config
//     load (lib/runtime/peer.Dial + Client.ValidateCapabilities) with
//     an operator-declared envelope of [sync] returns the canonical
//     "capabilities mismatch" error and never proceeds to instantiate a
//     Client — proof "a write-semantics the producer didn't advertise
//     is silently accepted at registration" is FALSE. This is the
//     IDENTICAL code path lib/control/config/stores.go::dialRemoteStores
//     runs at rimsky startup; a startup config with the same misshapen
//     envelope would cause the all-in-one container to exit non-zero
//     before /health (the same failure mode the in-process producer's
//     SSH-tunnel race surfaces — see Dockerfile.example's preamble for
//     why this test runs the producer in a container against rimsky and
//     a SEPARATE in-process producer for legs 3 + 4).
//
// The four legs together exhibit STORY-claim-producer-protocol's three
// falsifier failure modes:
//   - "A registered producer's Open is bypassed" → fails when no
//     `claim_resolution.commit` or `claim_resolution.abandon` event
//     lands (Open is the prerequisite for either terminal event).
//   - "Commit/Abandon/Release are called but the producer's effect is
//     canned" → fails when the terminal event doesn't land in legs 1/2
//     (the event is emitted AFTER the producer's verb returns OK), or
//     when the Release counter doesn't grow against the real claim_id
//     in leg 3.
//   - "a write-semantics the producer didn't advertise is silently
//     accepted at registration" → fails when leg 4's
//     ValidateCapabilities returns a non-error result for the [sync]
//     envelope.
//
// Test files are exempt from the Apache→AGPL import-direction lint
// (tools/license-check/imports.go::verifyImports), so this `_test.go`
// file may import the lib/services testcontainers harness AND the
// lib/runtime/peer registration code without putting the example's
// published Apache surface at risk — consumers who `go build` the
// example never pull in any test dependency.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestE2E_ExampleClaimProducerAgainstRunningRimsky brings up a docker
// network with the example claim-producer (built from
// `Dockerfile.example`) + a success executor stub + an erroring executor
// stub + the rimsky-all-in-one container, then exhibits each of the
// four protocol-surface properties STORY-claim-producer-protocol's
// Acceptance names.
//
// Build requirement: the rimsky-all-in-one image must be built locally
// (`make core-images`) before this test runs. The example producer's
// image is built ON DEMAND from `Dockerfile.example` via testcontainers
// FromDockerfile — no `make` target is required for it. A missing
// rimsky-all-in-one is a hard t.Fatal (the harness never t.Skip's), so
// a developer who hasn't run `make core-images` sees the
// missing-image error directly.
func TestE2E_ExampleClaimProducerAgainstRunningRimsky(t *testing.T) {
	// Not parallel: this scenario stands up a docker network + a Postgres
	// testcontainer + a rimsky-all-in-one container + three peer
	// containers (example producer + ok executor + err executor) + an
	// in-process producer on a host port. The cost is real, so the
	// in-process claimproducer_test.go keeps its fast shape and only
	// this gate pays the cross-stack price.
	ctx := context.Background()

	// 1. Shared docker network. The peer containers + rimsky-all-in-one
	//    + the postgres testcontainer all attach to it. The peer
	//    containers MUST be up BEFORE rimsky comes up because rimsky's
	//    control-api / scheduler / supervisor eager-dial every declared
	//    claim-producer and executor at startup and EXIT NON-ZERO if any
	//    is unreachable.
	netName := harness.NewNetwork(ctx, t)

	// 2. Example producer container on the network at alias
	//    `example-producer`. Built on demand from Dockerfile.example via
	//    testcontainers FromDockerfile. The build context is the
	//    rimsky-core repo root so the build can vendor the in-tree
	//    lib/protocols module via go.work.
	prodInternal := startExampleClaimProducerOnNetwork(ctx, t, netName, "example-producer")

	// 3. Two stub executors on the network (success + error). The
	//    harness ships container-based stubs at lib/services/test/
	//    stubexecutor, built on demand by FromDockerfile too — same
	//    ordering discipline as the example producer.
	okEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")
	errEndpoint := harness.StartErroringExecutorStubOnNetwork(ctx, t, netName, "exec-err")

	// 4. rimsky-all-in-one on the harness default (Postgres). All peers
	//    are reachable on stable in-network aliases by the time we
	//    bring up rimsky, so the eager startup handshake reaches them
	//    deterministically.
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		// The example producer advertises read_only; the operator's
		// envelope MUST be a non-empty subset of that. [read_only] is
		// the only honest declaration; sync / staged_async / blocking
		// would all be rejected by ValidateCapabilities at startup
		// (which leg 4 exhibits directly).
		harness.WithClaimProducer("example", prodInternal, "read_only"),
		harness.WithExecutor("ok", okEndpoint),
		harness.WithExecutor("err", errEndpoint),
		// Strict-`all` (the all-in-one image's baked default) would
		// require every executor's expected_attributes_schema to be in
		// the discovery cache before POST /v1/templates accepts the
		// template. The stubs advertise a permissive open schema so
		// `all` would also work, but `none` keeps the gate on the
		// producer + executor wiring under test, not the schema
		// freshness race.
		harness.WithRefValidationMode("none"),
	)

	// 5. SEPARATE in-process producer on a host port for legs 3 + 4.
	//    These legs are pure protocol-surface observations (Release
	//    directly via the wire client; the registration validator
	//    directly via peer.Dial + ValidateCapabilities); they do not
	//    drive rimsky and so are immune to the host-port-tunnel race
	//    that forces the container producer above.
	inProcPort := freeHostPort(t)
	inProcProd := startExampleProducerInProcess(t, inProcPort)

	// Run each acceptance leg as a sub-test against the SAME running
	// stack — the four legs are independent observations, so a single
	// bring-up is sufficient and a per-leg bring-up would only multiply
	// the bring-up cost.
	t.Run("Open_and_Commit_on_success_terminal", func(t *testing.T) {
		exerciseOpenCommitLeg(t, ep)
	})
	t.Run("Open_and_Abandon_on_failure_terminal", func(t *testing.T) {
		exerciseOpenAbandonLeg(t, ep)
	})
	t.Run("Release_RPC_lands_on_real_producer", func(t *testing.T) {
		exerciseReleaseLeg(t, inProcProd, inProcPort)
	})
	t.Run("Unadvertised_write_semantics_refused_at_registration", func(t *testing.T) {
		exerciseUnadvertisedWriteSemanticsLeg(t, inProcPort)
	})
}

// exerciseOpenCommitLeg deploys a template referencing the example
// producer (intent: r) on the success executor stub, creates an
// instance, and asserts:
//   - The node settles to `fresh` (terminal success) through the real
//     supervisor.
//   - A `claim_resolution.commit` event lands on /v1/events for this
//     instance with producer_name=example — proof rimsky's terminal
//     pipeline called the producer's Commit RPC and it returned
//     successfully.
//
// Proof for spec acceptance leg (a): "rimsky drives Commit at
// auto-terminal."
//
// The `claim_resolution.commit` event is emitted from
// lib/runtime/terminal_decision_forensics.go::emitTerminalForensics
// AFTER fireProducerVerb's `td.Producer.Commit(...)` call returns OK.
// Its presence is the load-bearing observable for "the producer's
// Commit verb really fired" — falsifier "Commit is called but the
// producer's effect is canned" fails here.
func exerciseOpenCommitLeg(t *testing.T, ep harness.RimskyEndpoint) {
	tplID := deployClaimTemplate(t, ep, "example-claim-commit", "ok", "r-commit")
	instanceID := createClaimInstance(t, ep, tplID, "ck-example-claim-commit")

	waitForNodeState(t, ep, instanceID, "worker", "fresh", 120*time.Second)
	requireEventKindWithProducer(t, ep, instanceID,
		"claim_resolution.commit", "example", 60*time.Second,
		"the supervisor's terminal pipeline must have called the example producer's Commit RPC and the RPC must have returned successfully (falsifier: Commit called but the producer's effect is canned)")
}

// exerciseOpenAbandonLeg deploys a second template referencing the
// example producer on the erroring executor stub, creates an instance,
// and asserts:
//   - The node settles to `failed` through the real supervisor.
//   - A `claim_resolution.abandon` event lands on /v1/events for this
//     instance with producer_name=example — proof rimsky's terminal
//     pipeline called the producer's Abandon RPC and it returned
//     successfully.
//
// Proof for spec acceptance leg (b): "on failure, Abandon."
func exerciseOpenAbandonLeg(t *testing.T, ep harness.RimskyEndpoint) {
	// The erroring stub returns Error{class: "stub/forced_error"}; the
	// template declares give_up policy on that class so the node-run
	// terminates failed (not retry-loop). Mirrors the
	// fs_held_swap_e2e_test.go pattern.
	tplID := deployClaimTemplateWithErrorPolicy(t, ep, "example-claim-abandon", "err", "r-abandon")
	instanceID := createClaimInstance(t, ep, tplID, "ck-example-claim-abandon")

	waitForNodeState(t, ep, instanceID, "worker", "failed", 120*time.Second)
	requireEventKindWithProducer(t, ep, instanceID,
		"claim_resolution.abandon", "example", 60*time.Second,
		"the supervisor's terminal pipeline must have called the example producer's Abandon RPC and the RPC must have returned successfully (falsifier: Abandon called but the producer's effect is canned)")
}

// exerciseReleaseLeg dials the SEPARATE in-process example producer
// through the EXACT same gRPC wiring rimsky's supervisor uses on a
// held-durable instance terminate (lib/runtime/peer.Client.Release) and
// asserts the producer's releaseCalls counter grew with the claim_id we
// passed.
//
// Release is invoked by rimsky's `ReleaseHeldDurableClaims` at instance
// terminate against held durable claims (asset pattern; requires the
// data_processing protocol the example producer does NOT advertise).
// We exhibit the protocol surface by driving the peer.Client.Release
// call site directly — same wire shape, same call into the producer's
// real handler, identical to what rimsky would do on a held-durable
// terminate.
//
// Proof for spec acceptance leg (c): "on lifecycle close, Release."
// Falsifier ("Release is called but the producer's effect is canned")
// fails when the counter does not grow or when the claim_id is dropped
// on the wire.
func exerciseReleaseLeg(t *testing.T, prod *Producer, prodPort int) {
	before := prod.Calls()

	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := peer.Dial(dialCtx, "example", fmt.Sprintf("127.0.0.1:%d", prodPort))
	if err != nil {
		t.Fatalf("peer.Dial against the in-process example producer: %v", err)
	}
	defer client.Close()

	const releaseClaimID = "release-test-claim-id-cabc1234"
	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	if rErr := client.Release(callCtx,
		claimproducer.ClaimID(releaseClaimID),
		[]byte("release-test-scope"),
		[]byte("release-test-address"),
	); rErr != nil {
		t.Fatalf("Client.Release against the example producer: %v (the producer's Release handler must accept the verb and return without error — falsifier: rimsky drives Release but the producer is unreachable / canned)", rErr)
	}

	after := prod.Calls()
	if after.Release <= before.Release {
		t.Fatalf("Release count did NOT grow on the in-process producer: before=%d after=%d — the peer client did not call the producer's Release handler (falsifier: the verb's effect was canned)",
			before.Release, after.Release)
	}

	// Falsifier guard: the verb landed against the exact claim_id we
	// passed. A canned handler that ignores its input would record an
	// empty / stale claim_id; the assertion rules that out.
	releaseIDs := prod.ReleaseClaimIDs()
	if len(releaseIDs) == 0 {
		t.Fatalf("Release landed but no claim_ids were recorded — internal counter inconsistency")
	}
	gotID := releaseIDs[len(releaseIDs)-1]
	if gotID != releaseClaimID {
		t.Fatalf("Release landed with claim_id %q, want %q — the producer's effect must NOT be canned; it must receive the claim_id rimsky passed",
			gotID, releaseClaimID)
	}
}

// exerciseUnadvertisedWriteSemanticsLeg dials the in-process example
// producer via the EXACT production code rimsky uses at startup config
// load (lib/runtime/peer.Dial + Client.ValidateCapabilities) and
// exhibits the registration refusal of an operator envelope claiming a
// write-semantics the producer's Capabilities never advertised.
//
// The producer advertises ONLY `read_only`. Calling
// ValidateCapabilities with an operator-declared envelope of `[sync]`
// MUST return an error citing "capabilities mismatch" — the same error
// rimsky's startup dialRemoteStores returns; a startup config with the
// same misshapen envelope causes the all-in-one container to exit
// non-zero before /health.
//
// Proof for spec acceptance leg (d): "a template referencing a
// write-semantics the producer doesn't advertise is refused at
// registration." Falsifier ("a write-semantics the producer didn't
// advertise is silently accepted at registration") fails when the call
// returns nil error.
func exerciseUnadvertisedWriteSemanticsLeg(t *testing.T, prodPort int) {
	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// peer.Dial is the same function lib/control/config/stores.go calls
	// at startup. It runs the Capabilities handshake and caches the
	// advertised envelope on the Client.
	client, err := peer.Dial(dialCtx, "example", fmt.Sprintf("127.0.0.1:%d", prodPort))
	if err != nil {
		t.Fatalf("peer.Dial against the in-process example producer: %v (the dial must succeed — the registration refusal we are proving happens at ValidateCapabilities, AFTER Capabilities returns successfully)", err)
	}
	defer client.Close()

	// Operator-declared envelope claiming `sync` — a write-semantics
	// the producer NEVER advertised. ValidateCapabilities is the
	// subset check rimsky runs at startup against every claim_producers
	// entry's `write_semantics_allowed` field; mismatch is a hard
	// error.
	declared := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	vErr := client.ValidateCapabilities(declared)
	if vErr == nil {
		t.Fatalf("ValidateCapabilities accepted an operator envelope ([sync]) that the producer NEVER advertised (producer's Capabilities returns [read_only] only) — the falsifier fires (\"a write-semantics the producer didn't advertise is silently accepted at registration\"). A startup config with this envelope would cause the rimsky-all-in-one container to exit non-zero before /health flips to 200")
	}

	// The error message must name "capabilities mismatch" (the
	// canonical startup-registration error in
	// lib/runtime/peer/client.go) so an operator can diagnose the
	// misshapen envelope from logs alone.
	msg := strings.ToLower(vErr.Error())
	if !strings.Contains(msg, "capabilities mismatch") {
		t.Fatalf("ValidateCapabilities rejected the envelope but the error does not name the canonical failure mode (\"capabilities mismatch\"): %v", vErr)
	}
}

// --- event observation helpers -----------------------------------------------

// requireEventKindWithProducer polls GET /v1/events?instance_id=...&kind=...
// until at least one event of the given kind appears whose
// payload.producer_name field equals `wantProducer`, or the deadline
// elapses. Fails hard on timeout — the event landing is the load-bearing
// observable, never a skip. On failure, dumps the kinds + producer names
// that DID land on the instance so the developer can diagnose whether
// the wrong producer fired vs the value path never engaged at all.
func requireEventKindWithProducer(t *testing.T, ep harness.RimskyEndpoint, instanceID, kind, wantProducer string, deadline time.Duration, why string) {
	t.Helper()
	end := time.Now().Add(deadline)
	path := fmt.Sprintf("/v1/events?instance_id=%s&kind=%s", instanceID, kind)
	var lastStatus int
	var lastBody string
	for time.Now().Before(end) {
		statusCode, raw := ep.GetJSON(t, path, "")
		lastStatus, lastBody = statusCode, string(raw)
		if statusCode == http.StatusOK {
			var resp struct {
				Events []struct {
					Kind    string          `json:"kind"`
					Payload json.RawMessage `json:"payload"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, e := range resp.Events {
					if e.Kind != kind {
						continue
					}
					var p map[string]any
					if jErr := json.Unmarshal(e.Payload, &p); jErr != nil {
						continue
					}
					if pn, ok := p["producer_name"].(string); ok && pn == wantProducer {
						return
					}
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	dump := dumpEventKindsForInstance(t, ep, instanceID)
	t.Fatalf("event kind %q with producer_name=%q never landed on the event log for instance %s within %v (last GET status=%d body=%s) — %s\nobserved event kinds on this instance: %v",
		kind, wantProducer, instanceID, deadline, lastStatus, lastBody, why, dump)
}

// dumpEventKindsForInstance fetches the unfiltered event feed for an
// instance and returns the sorted set of distinct kinds. Used by
// requireEventKindWithProducer to enrich the failure message.
func dumpEventKindsForInstance(t *testing.T, ep harness.RimskyEndpoint, instanceID string) []string {
	t.Helper()
	var (
		statusCode int
		raw        []byte
	)
	for attempt := 0; attempt < 10; attempt++ {
		statusCode, raw = ep.GetJSON(t, "/v1/events?instance_id="+instanceID+"&limit=500", "")
		if statusCode == http.StatusOK {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if statusCode != http.StatusOK {
		return []string{fmt.Sprintf("<GET /v1/events failed after retries: %d %s>", statusCode, string(raw))}
	}
	var resp struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return []string{fmt.Sprintf("<decode failed: %v>", err)}
	}
	seen := map[string]int{}
	for _, e := range resp.Events {
		seen[e.Kind]++
	}
	out := make([]string, 0, len(seen))
	for k, n := range seen {
		out = append(out, fmt.Sprintf("%s(%d)", k, n))
	}
	return out
}

// --- template helpers --------------------------------------------------------

// deployClaimTemplate posts a single-node template referencing the
// example producer (intent: r, the producer's only honest intent) on
// the given executor. The selector is per-template-distinct so each
// instance's Open lands on a fresh claim_handle row, not a dedup of a
// prior one.
//
// `holds:` is intentionally absent — a non-held claim lets the
// terminal pipeline call the producer's Commit/Abandon directly via
// runner_terminal_release.releaseClaim, which is exactly the verb-
// firing path Acceptance (a) and (b) measure.
func deployClaimTemplate(t *testing.T, ep harness.RimskyEndpoint, name, executor, selector string) string {
	t.Helper()
	return deployClaimTemplateInternal(t, ep, name, executor, selector, false)
}

// deployClaimTemplateWithErrorPolicy is deployClaimTemplate plus the
// `error_types: { stub/forced_error: give_up }` chain so the erroring
// stub's emitted error class drives the node to a deterministic
// `failed` terminal — that's what fires the auto-terminal Abandon
// against the producer.
func deployClaimTemplateWithErrorPolicy(t *testing.T, ep harness.RimskyEndpoint, name, executor, selector string) string {
	t.Helper()
	return deployClaimTemplateInternal(t, ep, name, executor, selector, true)
}

// deployClaimTemplateInternal builds + deploys the worker-with-store
// template shape used by the Commit and Abandon legs. The
// withErrorPolicy flag wires the stub-error class to give_up so the
// failure path terminates without a retry loop.
func deployClaimTemplateInternal(t *testing.T, ep harness.RimskyEndpoint, name, executor, selector string, withErrorPolicy bool) string {
	t.Helper()
	node := map[string]any{
		"type":     "worker",
		"executor": executor,
		"stores": []map[string]any{
			{
				"name":     "example",
				"selector": selector,
				"intent":   "r",
				"alias":    "claim",
			},
		},
	}
	if withErrorPolicy {
		node["error_types"] = map[string]any{
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
			"nodes":                 []map[string]any{node},
		},
	}
	statusCode, raw := ep.PostJSON(t, "/v1/templates", body)
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/templates (%s): %d %s", name, statusCode, string(raw))
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
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createClaimInstance POSTs a new instance and returns its instance_id.
func createClaimInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	statusCode, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", statusCode, string(raw))
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
// node reaches `want` (or the deadline). Mirrors
// fs_held_swap_e2e_test.go::waitForNodeState. Fails hard on timeout —
// "the node reached the terminal we expected" is the load-bearing
// observable, never a skip.
func waitForNodeState(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType, want string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		statusCode, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if statusCode == http.StatusOK {
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
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach %q within %v; last state=%q",
		nodeType, instanceID, want, deadline, lastState)
}

// --- example producer container bring-up ------------------------------------

// examplePProducerBuildMu serializes the testcontainers FromDockerfile
// build of the example producer so parallel runs of the e2e test don't
// race on the same image tag (mirrors the executor-stub harness's
// stubBuildMu pattern). go test runs by default with parallel sub-tests
// inside ONE binary, but a developer may invoke `go test ./examples/...`
// across multiple modules — the mutex makes the build serialise within
// any single process.
var exampleProducerBuildMu sync.Mutex

// startExampleClaimProducerOnNetwork builds (on first use) and starts
// the example claim-producer in a container on the given docker network
// with the given alias, returning the in-network endpoint
// (`<alias>:9400`) that rimsky's claim-producer registry dials.
//
// The image is built on demand from this directory's Dockerfile.example
// via testcontainers FromDockerfile with the repo root as the build
// context — same pattern as the overlap producer
// (lib/services/test/harness/claimproducer_custom.go) and the
// executor-stub harness. KeepImage=true so a repeated run reuses the
// cached layer.
func startExampleClaimProducerOnNetwork(ctx context.Context, t *testing.T, networkName, alias string) (endpoint string) {
	t.Helper()
	exampleProducerBuildMu.Lock()
	defer exampleProducerBuildMu.Unlock()

	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:    repoRoot(),
			Dockerfile: "examples/claimproducer/Dockerfile.example",
			Repo:       "rimsky-example/claim-producer",
			Tag:        "latest",
			KeepImage:  true,
		}),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithExposedPorts("9400/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9400/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start example claim-producer: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return alias + ":9400"
}

// repoRoot returns the rimsky-core repo root (the directory containing
// go.work), derived from this file's own location
// (examples/claimproducer/main_e2e_test.go) so it is independent of the
// test's working directory. The Docker build context for the example
// producer is the repo root because the build copies in lib/protocols +
// the examples module via go.work — see Dockerfile.example.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// --- in-process producer + port helpers --------------------------------------

// freeHostPort grabs an OS-assigned TCP port and returns it. The brief
// close-then-reuse race is acceptable for an in-process test fixture
// (matches the pattern in examples/executor/main_e2e_test.go::freeHostPort
// and examples/publisher/main_e2e_test.go::freeHostPort).
func freeHostPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if cerr := lis.Close(); cerr != nil {
		t.Fatalf("close listener: %v", cerr)
	}
	return port
}

// startExampleProducerInProcess stands up an in-process Producer on the
// given host port for legs 3 + 4 (direct gRPC observation). The
// in-process producer is NOT registered with rimsky — those legs do not
// drive a rimsky dispatch, so the SSH-host-port-tunnel race against
// rimsky's startup eager dial is irrelevant here. Cleanup (graceful
// Stop) is registered via t.Cleanup.
func startExampleProducerInProcess(t *testing.T, port int) *Producer {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	prod := newProducer()
	genv1.RegisterClaimProducerServer(srv, prod)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	// Poll-dial to confirm the gRPC server is up before returning, so
	// the leg-3 / leg-4 dials don't race the listener.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return prod
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("in-process example producer did not become dialable at %s within 10s", addr)
	return nil
}
