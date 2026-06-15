// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Per-value write-semantics behavioral coverage — the reader-vs-writer
// concurrency consequences of each realized WriteSemantics value, driven
// end-to-end through the real supervisor acquisition flow against
// testcontainers Postgres.
//
// Background — what is and isn't already covered:
//
//   - ModeCoexists (foundation/locks/conflict.go) is the pure predicate;
//     conflict_test.go::TestModeCoexistsMatrix covers it as a function.
//   - claim_scope_conflict_race_test.go drives the sync↔sync writer race
//     end-to-end (two writers conflict; one wins).
//
// What had NO end-to-end behavioral cover before this file is the
// per-value branch ModeCoexists takes inside the REAL acquisition path
// (runner_acquire_claims.go::evaluateClaimScopeConflict): that a
// staged_async writer and a reader of the same claim-scope COEXIST, while
// a blocking_async (or sync) writer SERIALIZES a same-scope reader behind
// its release. Two writers conflict under all three values (the w×w
// single-writer-per-scope rule), so the value-distinguishing observable
// is reader-vs-writer, not writer-vs-writer.
//
// How acquisition is driven: NoSupervisor=true; the scheduler enqueues
// the dispatch rows; the test then drives runtime.RunNode by hand, one
// call per contender, exactly as named_lock_limit_test.go does. Both the
// writer node and the reader node target a BARRIER executor that blocks
// inside Execute until the test releases it — so a holder sits mid-flight
// with its rimsky_claim_handles row committed (state='active'), making the
// simultaneous-active-row count directly observable.
//
// Coupling to the per-value enforcement (why these FAIL if the
// ModeCoexists branch were broken):
//
//   - evaluateClaimScopeConflict re-loads the active same-scope holder and
//     evaluates ModeCoexists(candidate.Intent, holderRWS, holderIntent,
//     holderRWS). The candidate is a READER (intent="r"); the holder is a
//     parked WRITER (intent="rw").
//   - staged_async lands in the async block of ModeCoexists: r×rw → coexist
//     → the reader passes the predicate, Opens, and INSERTs its own active
//     row. Observable: TWO active claim_scope rows for one (producer,
//     scope) at the same instant, both runs entering the barrier. If the
//     async-block branch were removed (treated like sync), the reader would
//     BAIL and the active-row count would stay at 1 — the assertion flips.
//   - blocking_async lands in the SYNC block (isSync returns true for it):
//     r×rw → conflict → the reader BAILS (Ran=false) while the writer holds.
//     Only after the writer releases does a reader retry acquire (Ran=true).
//     Observable serialization: at most one active row exists at the gated
//     instant, and the reader's dispatch row stays pending+unclaimed. If
//     blocking_async were (incorrectly) classified into the async block, the
//     reader would coexist and the "reader bails while writer holds"
//     assertion would flip.
//
// The two cases are mirror images through the SAME code path; together they
// prove the branch is load-bearing in both directions, not merely exercised.
package locks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

const writeSemanticsSelector = "/contended-ws"

// activeScopeRowCount returns the number of state='active' claim_scope
// holder rows for (producerName, the contended selector). This is the
// exact ledger ModeCoexists reads through ListByProducerClaimScope; we
// read it directly to observe coexistence vs serialization.
func activeScopeRowCount(t *testing.T, h *scenario.Harness, producerName string) int {
	t.Helper()
	var n int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_handles
		  WHERE lock_kind = 'claim_scope' AND producer_name = $1 AND state = 'active'`,
		[]any{producerName}, &n)
	return n
}

// startWriteSemanticsHarness brings up a stub store advertising exactly
// one realized write-semantics value, a barrier executor, and a two-node
// graph (a writer node and a reader node) both claiming the SAME selector
// against that store. Returns the harness, the dialed store registry, the
// barrier, the writer/reader node ids, and a RunArgs factory.
//
// Writer node intent = "rw"; reader node intent = "r". Both nodes share
// the byte-equal selector, so they contend on one claim-scope — the
// reader-vs-writer pairing whose outcome the realized value determines.
func startWriteSemanticsHarness(
	t *testing.T, value claimproducer.WriteSemantics,
) (*scenario.Harness, *barrierExecutor, shared.UUID, shared.UUID, func(string) runtime.RunArgs, *executor.ClientPool) {
	t.Helper()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{value}},
	})
	t.Cleanup(teardown)

	barrier := newBarrierExecutor()
	execAddr := startBarrierExecutor(t, barrier)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{value}},
				},
			},
		},
		ExtraExecutors: map[string]executor.Endpoint{
			"barrier": {Transport: "grpc", URL: execAddr},
		},
	})

	// @deliberate: Writer template: a single node holding a read-write claim on the
	// contended selector. Reader template: a single node holding a
	// read-only claim on the SAME selector. Separate templates +
	// instances so each contender owns its own root frame / dispatch row /
	// node row and two manual supervisors can race without frame-engine
	// coupling (same isolation the named-lock + scope-race tests use).
	writerTid := h.DeployTemplate(node.TemplateSpec{
		Name: "ws-writer", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "writer", Executor: "barrier"},
				scenario.WithStores(scenario.WriteClaimRef("content", writeSemanticsSelector)),
			),
		},
	})
	readerTid := h.DeployTemplate(node.TemplateSpec{
		Name: "ws-reader", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "reader", Executor: "barrier"},
				scenario.WithStores(scenario.ClaimRef("content", writeSemanticsSelector)),
			),
		},
	})

	writerIID := h.CreateInstance(writerTid, "ck-ws-writer", map[string]any{})
	readerIID := h.CreateInstance(readerTid, "ck-ws-reader", map[string]any{})

	writerNode := h.FindNode(writerIID, "writer")
	readerNode := h.FindNode(readerIID, "reader")
	require.NotNil(t, writerNode)
	require.NotNil(t, readerNode)
	require.True(t, h.WaitForDispatch(writerNode.ID, 15*time.Second), "writer dispatch row never appeared")
	require.True(t, h.WaitForDispatch(readerNode.ID, 15*time.Second), "reader dispatch row never appeared")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// @deliberate: One shared registry holding a peer.Client to the loopback stub. Dial
	// performs the Capabilities() handshake, so the dialed client's caps
	// carry exactly `value` — the realized-value envelope check in
	// peer/client.go::Open passes. Client + conn are concurrency-safe so two
	// RunNode goroutines can share both.
	client, err := peer.Dial(h.Ctx, "content", "grpc://"+endpoint, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	reg := locks.NewRegistry()
	reg.Add("content", client)

	makeArgs := func(supID string) runtime.RunArgs {
		return runtime.RunArgs{
			Persist:           h.Persist,
			Queue:             h.Queue,
			ClaimHandles:      h.Persist.ClaimHandles(),
			AdvisoryLocker:    h.Driver.AdvisoryLocker(),
			StoreRegistry:     reg,
			Clock:             shared.SystemClock{},
			Logger:            shared.SilentLogger{},
			SupervisorID:      supID,
			AcceptedExecutors: []string{"barrier"},
			AcceptedStores:    []string{"content"},
			Pool:              pool,
			Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
				"barrier": {Transport: "grpc", URL: execAddr},
			}),
			HeartbeatInterval: 1 * time.Second,
		}
	}

	return h, barrier, writerNode.ID, readerNode.ID, makeArgs, pool
}

// TestWriteSemanticsStagedAsyncReaderCoexistsWithWriter proves the
// staged_async per-value behavior end-to-end: a reader and a writer on the
// same claim-scope COEXIST. While the writer is parked mid-flight holding
// its active claim_scope row, the reader's full RunNode cycle ACQUIRES
// (Ran=true) and adds a second active row — two simultaneous active rows
// for one (producer, scope).
//
// Coupling: the second active row exists ONLY because ModeCoexists takes
// the async-block r×rw=coexist branch for staged_async inside
// evaluateClaimScopeConflict. If staged_async were (mis)classified into the
// sync block, the reader would bail and the count would stay at 1.
func TestWriteSemanticsStagedAsyncReaderCoexistsWithWriter(t *testing.T) {
	t.Parallel()

	h, barrier, _, readerNodeID, makeArgs, _ := startWriteSemanticsHarness(t, claimproducer.WriteSemanticsStagedAsync)

	require.Equal(t, 0, activeScopeRowCount(t, h, "content"),
		"precondition: no scope holders before any RunNode")

	// @deliberate: Launch the writer; it Opens the rw claim (active row #1) and blocks in
	// the barrier executor.
	writer := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeArgs("sup-writer"), nil)
		writer <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)

	require.Equal(t, 1, activeScopeRowCount(t, h, "content"),
		"writer parked mid-flight: exactly one active claim_scope row")

	// @deliberate: Launch the reader. Under staged_async the reader-vs-writer pair
	// coexists, so the reader passes the conflict predicate, Opens its own
	// claim (active row #2), and enters the barrier too — proving genuine
	// acquisition rather than a bail.
	reader := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeArgs("sup-reader"), nil)
		reader <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)

	// @deliberate: COEXISTENCE: two active claim_scope rows for the same (producer,
	// scope) exist simultaneously — the writer's and the reader's. This is
	// the load-bearing observable; it is impossible under sync /
	// blocking_async (the reader would have bailed without an active row).
	require.Equal(t, 2, activeScopeRowCount(t, h, "content"),
		"staged_async COEXISTENCE: reader and writer hold simultaneously (two active rows)")

	barrier.freeOne()
	barrier.freeOne()
	rW := <-writer
	rR := <-reader
	require.NoError(t, rW.err, "writer RunNode error")
	require.NoError(t, rR.err, "reader RunNode error")
	require.True(t, rW.out.Ran, "writer must have run")
	require.True(t, rR.out.Ran,
		"staged_async: reader must ACQUIRE-and-run while writer held (coexistence)")

	// @deliberate: Disposition: after both terminal, every active scope row releases.
	requireScopeRowCountEventually(t, h, "content", 0, 5*time.Second)

	_ = readerNodeID
}

// TestWriteSemanticsBlockingAsyncSerializesReaderBehindWriter proves the
// blocking_async per-value behavior end-to-end: a reader of the same
// claim-scope is SERIALIZED behind a writer. While the writer is parked
// mid-flight holding its active row, the reader's full RunNode cycle BAILS
// (Ran=false) and never reaches the barrier; its dispatch row stays
// pending+unclaimed. Only after the writer releases can the reader acquire.
//
// Coupling: the reader bail-while-writer-holds is the serialization
// observable. blocking_async lands in the SYNC block of ModeCoexists
// (isSync returns true for it), so r×rw=conflict. If blocking_async were
// (mis)classified into the async block, the reader would coexist (Ran=true,
// two active rows) and the bail assertion below would flip.
func TestWriteSemanticsBlockingAsyncSerializesReaderBehindWriter(t *testing.T) {
	t.Parallel()

	h, barrier, _, readerNodeID, makeArgs, _ := startWriteSemanticsHarness(t, claimproducer.WriteSemanticsBlockingAsync)

	require.Equal(t, 0, activeScopeRowCount(t, h, "content"),
		"precondition: no scope holders before any RunNode")

	// @deliberate: Launch the writer; it Opens the rw claim (active row #1) and blocks in
	// the barrier executor.
	writer := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeArgs("sup-writer"), nil)
		writer <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)

	require.Equal(t, 1, activeScopeRowCount(t, h, "content"),
		"writer parked mid-flight: exactly one active claim_scope row")

	// @constraint: While the writer holds, the reader's full RunNode cycle must BAIL:
	// the candidate is selected and the dispatch row claimed, but
	// evaluateClaimScopeConflict sees the active rw holder and ModeCoexists
	// returns false (sync block) → the per-candidate tx rolls back. RunNode
	// reports Ran=false and the barrier executor is NEVER reached.
	rGated := runReaderOnce(t, h, makeArgs("sup-reader"))
	require.NoError(t, rGated.err, "blocking_async: gated reader RunNode must not error (a soft serialize bail)")
	require.False(t, rGated.out.Ran,
		"blocking_async SERIALIZATION: reader must BAIL (Ran=false) while writer holds the same claim-scope")

	// @constraint: serialization observable: still exactly one active row (the writer's);
	// the reader's bail left nothing behind.
	require.Equal(t, 1, activeScopeRowCount(t, h, "content"),
		"after the reader bails, still exactly one active claim_scope row (writer)")

	// @constraint: The reader's dispatch row must remain pending+unclaimed (the bail
	// rolled back the dispatch claim), so it is re-acquirable once the
	// writer releases.
	requireDispatchPending(t, h, readerNodeID)

	// @constraint: Release the writer → it terminals → its active row releases.
	barrier.freeOne()
	rW := <-writer
	require.NoError(t, rW.err, "writer RunNode error")
	require.True(t, rW.out.Ran, "writer must have run")
	requireScopeRowCountEventually(t, h, "content", 0, 5*time.Second)

	// @deliberate: Now the scope is free: the formerly-gated reader can acquire and run.
	// It enters the barrier (proving genuine acquisition, not a bail),
	// bringing the active count back to 1 — second-after-first serialization.
	reader := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeArgs("sup-reader"), nil)
		reader <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)
	require.Equal(t, 1, activeScopeRowCount(t, h, "content"),
		"reader acquired AFTER writer released (serialized second-after-first)")
	barrier.freeOne()
	rR := <-reader
	require.NoError(t, rR.err, "reader second RunNode error")
	require.True(t, rR.out.Ran,
		"blocking_async: reader must acquire and run AFTER the writer released the scope")

	requireScopeRowCountEventually(t, h, "content", 0, 5*time.Second)
}

// runReaderOnce drives one synchronous RunNode cycle for the reader
// contender and returns the result.
func runReaderOnce(t *testing.T, h *scenario.Harness, args runtime.RunArgs) runResult {
	t.Helper()
	out, err := runtime.RunNode(h.Ctx, args, nil)
	return runResult{out: out, err: err}
}

// requireScopeRowCountEventually polls the active claim_scope row count
// until it matches want or the timeout elapses. The terminal-release tx
// commits slightly after RunNode returns on the read connection, so a short
// poll removes that sampling race (mirrors requireNamedLockCountEventually).
func requireScopeRowCountEventually(t *testing.T, h *scenario.Harness, producerName string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		got = activeScopeRowCount(t, h, producerName)
		if got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, want, got,
		"claim_scope active row count for %q did not settle to %d within %s", producerName, want, timeout)
}
