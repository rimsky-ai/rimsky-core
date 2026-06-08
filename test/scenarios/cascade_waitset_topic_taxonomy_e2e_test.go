// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Acceptance gate for S-cascade-waitset-topic-taxonomy (spec
// 2026-06-06-comprehensive-gap-closure, plan AG-TEMPLCASCADE-7). The
// per-pass tests pin the same behavior at component altitude — the
// migration probe TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy
// (lib/foundation/persistence/{postgres,sqlite}/wait_set_topic_kind_test.go)
// proves the broadened CHECK admits the full taxonomy, and the unit
// test TestWaitSetTopicKindFor_FullTaxonomy
// (lib/runtime/runner_terminal_test.go) pins waitSetTopicKindFor to one
// bucket per top-level signal kind. THIS test proves the user-outcome
// story holds end-to-end against the REAL assembled product: a real
// control-api over HTTP deploys a template, the real scheduler + frame
// engine + supervisor + stub-executor dispatch drive the cascade, and
// the wait-set ledger (rimsky_wait_set) is read back through the real
// persistence surface (InTx) — the same surface the runtime writes
// through. Testcontainers Postgres with migration 006 applied.
//
// Story: an operator inspects the wait-set ledger for a receiver gated
// on distinct signal classes and sees the actual class recorded per
// edge, because topic_kind now spans the full 5-value taxonomy rather
// than collapsing terminal/transient/message onto the lossy `state`
// bucket.
//
// What this drives through the REAL value path:
//   - terminal class: `term_sender` settles via terminal/success; the
//     terminal cascade (cascadeSubscribersStaleInTx via emitSignalInTx)
//     inserts a wait-set row gating the receiver on term_sender's run
//     with topic_kind = waitSetTopicKindFor("terminal/success") =
//     "terminal".
//   - transient class: `transient_sender` errors and the retry policy
//     emits transient/retry/<n>/<class> through the SAME cascade
//     chokepoint (emitSignalInTxOnce in runner_error_policy.go), so the
//     receiver gets a wait-set row with topic_kind =
//     waitSetTopicKindFor("transient/retry/...") = "transient". The
//     receiver is genuinely GATED on this undrained transient row during
//     the retry window — we poll until it appears and read it then.
//   - message class: messages cross-cut and carry NO sender node-run, so
//     the architecture stale-marks message receivers rather than gating
//     them on a wait-set blocker (sender_run_id is NOT NULL and FKs to
//     rimsky_node_runs — a message has no such run). The message
//     topic_kind is therefore proven by inserting a real wait-set row
//     with TopicKind="message" through the SAME WaitSet().Insert call
//     the runtime cascade uses, keyed on real receiver/sender runs +
//     the real frame, and reading it back. This is exactly the gate's
//     stated proof: "Assert the broadened CHECK is in force (the row
//     inserted without rejection)" — pre-006 the CHECK
//     (topic_kind IN ('state','attribute','event')) rejects "message"
//     with a constraint violation.
//
// Decisive RED-vs-GREEN discriminators:
//   - The receiver's terminal-gated row reads topic_kind="terminal"
//     (RED today: "state") and the transient-gated row reads
//     topic_kind="transient" (RED today: "state"). With the lossy
//     3-bucket collapse both would read "state" and collapse onto a
//     single bucket — the no-two-classes-collapse assertion fails.
//   - The message-kind WaitSet().Insert is ACCEPTED (RED today: the
//     unbroadened CHECK rejects topic_kind="message" with a
//     constraint-violation error).
//   - The gated run completes end to end (receiver reaches fresh) with
//     the broadened discriminator in place — drain/dedupe behavior
//     unchanged.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAcceptance_WaitSetTopicKindTaxonomy(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// term_sender settles fresh once → emits terminal/success, gating
	// term_receiver under topic_kind="terminal" before draining it
	// (settled-state drain) so term_receiver completes end to end.
	h.Stub.WhenType("term_sender").Success(map[string]any{"t": 1}, true, "term")
	h.Stub.WhenType("term_receiver").Success(map[string]any{"r": 1}, true, "rcv")

	// transient_sender errors with class `flaky`; the stub prefixes a
	// single-segment class with `stub/`, so the wire error_class is
	// `stub/flaky`. transient_receiver's gating subscription is on the
	// transient/retry/* class, so each retry of transient_sender emits a
	// transient/retry/<n>/stub/flaky signal that gates transient_receiver
	// under topic_kind="transient". The retry runs with a deliberate delay
	// (and a 100-deep retry chain) so transient_receiver stays observably
	// GATED on the undrained transient row for the lifetime of the test —
	// this is the story's "while the receiver is gated" snapshot. (A
	// transient/retry/* gate keyed on a superseded retry run is the
	// genuine "still gated" state; the broadened discriminator does not
	// change that drain behavior.)
	h.Stub.WhenType("transient_sender").Error("flaky", map[string]any{"hint": "transient"})

	// Two receivers, each gated on a distinct signal class:
	//   - term_receiver  ← term_sender via terminal/* → topic_kind="terminal";
	//     drains normally and reaches fresh end to end.
	//   - transient_receiver ← transient_sender via transient/retry/* →
	//     topic_kind="transient"; intentionally held gated so the ledger can
	//     be read mid-gate.
	// frame: in keeps each cascade joining the running frame.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "waitset-topic-taxonomy", Version: "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "term_sender", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "transient_sender",
				Executor: "stub",
				// A long retry chain with a real per-attempt delay keeps
				// transient_sender retrying (and re-emitting transient/retry
				// signals) for the test's duration, so transient_receiver
				// stays gated on an undrained transient row throughout.
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Policy: []node.PolicyAction{
						{Action: "retry", Count: 100, BaseDelayMs: 500},
					}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "term_receiver", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "term_sender", Type: "terminal/*", Frame: "in"},
				),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "transient_receiver", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "transient_sender", Type: "transient/retry/*", Frame: "in"},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-waitset-topic-taxonomy", map[string]any{})

	termReceiver := h.FindNode(iid, "term_receiver")
	transientReceiver := h.FindNode(iid, "transient_receiver")
	require.NotNil(t, termReceiver)
	require.NotNil(t, transientReceiver)

	// ---- transient class: transient_receiver GATED on an undrained
	// transient row. Poll until a topic_kind="transient" wait-set row for
	// it exists and is still undrained (mid-gate on the retrying
	// transient_sender). Reading it here satisfies the story's "while the
	// receiver is gated, inspect the wait-set ledger." The returned
	// run/frame ids are reused as the real keys for the message-row insert.
	transientFrame, transientReceiverRun, transientSenderRun :=
		waitForUndrainedReceiverWaitSetRow(t, h, transientReceiver.ID, "transient", 30*time.Second)
	require.NotEqual(t, shared.UUID{}, transientReceiverRun,
		"transient_receiver must be gated on an undrained topic_kind=transient wait-set row "+
			"(transient/retry/* edge must record topic_kind=transient, not the lossy `state` bucket)")

	// ---- terminal class: term_sender's settlement gated term_receiver
	// under topic_kind="terminal". Drained rows remain queryable (drain
	// marks drained_at rather than deleting), so this row is observable
	// regardless of frame timing. Poll for it.
	require.True(t, waitForReceiverWaitSetTopicKind(t, h, termReceiver.ID, "terminal", 30*time.Second),
		"term_receiver must carry a topic_kind=terminal wait-set row from the terminal/* edge "+
			"(terminal/success must record topic_kind=terminal, not `state`)")

	// ---- message class: messages cross-cut and carry NO sender node-run,
	// so the runtime stale-marks message receivers rather than gating them
	// on a wait-set blocker (sender_run_id is NOT NULL and FKs to
	// rimsky_node_runs — a message has no such run). The message topic_kind
	// is therefore proven by inserting a real wait-set row with
	// TopicKind="message" through the SAME WaitSet().Insert call the
	// runtime cascade uses, keyed on the real receiver/sender runs + frame
	// observed above. Pre-006 this insert is REJECTED by the CHECK
	// (topic_kind IN ('state','attribute','event')); post-006 it is
	// accepted. We then read it back and confirm it reads "message".
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		return h.Persist.WaitSet().Insert(h.Ctx, persistence.WaitSetRow{
			FrameID:           transientFrame,
			ReceiverRunID:     transientReceiverRun,
			SenderRunID:       transientSenderRun,
			TopicKind:         "message",
			SubscriptionScope: "instance",
		}, tx)
	}), "the broadened topic_kind CHECK (migration 006) must admit a message-kind "+
		"wait-set row without rejection")

	require.True(t, waitForReceiverWaitSetTopicKind(t, h, transientReceiver.ID, "message", 5*time.Second),
		"the inserted topic_kind=message row must read back as `message`, "+
			"proving the message signal class maps to its own bucket (not `state`)")

	// ---- no two distinct signal classes collapse onto the same bucket.
	// transient_receiver's runs now carry BOTH a transient-gated row and
	// the message-kind row; they must occupy DISTINCT topic_kind values,
	// and term_receiver's terminal-gated row must read "terminal" — three
	// distinct, kind-named buckets. Under the lossy 3-bucket collapse
	// terminal/transient/message all folded onto "state", which these
	// assertions forbid.
	transientKinds := distinctReceiverWaitSetTopicKinds(t, h, transientReceiver.ID)
	require.Contains(t, transientKinds, "transient", "transient-gated edge must record topic_kind=transient")
	require.Contains(t, transientKinds, "message", "message-kind row must record topic_kind=message")
	require.NotContains(t, transientKinds, "state",
		"transient/message classes must not collapse onto the legacy `state` bucket")

	termKinds := distinctReceiverWaitSetTopicKinds(t, h, termReceiver.ID)
	require.Contains(t, termKinds, "terminal", "terminal-gated edge must record topic_kind=terminal")
	require.NotContains(t, termKinds, "state",
		"terminal class must not collapse onto the legacy `state` bucket")

	// The three classes land on three distinct, kind-named values (no
	// collapse): transient, message, and terminal are each observed across
	// the gated receivers, none folded onto a shared bucket.
	allKinds := map[string]struct{}{}
	for _, k := range transientKinds {
		allKinds[k] = struct{}{}
	}
	for _, k := range termKinds {
		allKinds[k] = struct{}{}
	}
	for _, want := range []string{"transient", "message", "terminal"} {
		_, ok := allKinds[want]
		require.True(t, ok, "topic_kind %q must be observed across the gated receivers", want)
	}

	// ---- a gated run completes correctly end to end: term_receiver was
	// gated on term_sender's terminal row, the settled-state drain released
	// the gate, and it dispatches to fresh. This proves the broadened
	// discriminator leaves drain/dedupe behavior unchanged for a normally-
	// draining gate. (transient_receiver stays intentionally gated on the
	// retrying transient_sender — the "while the receiver is gated"
	// snapshot above — so it is not required to complete.)
	require.True(t, h.WaitForNodeState(termReceiver.ID, cascade.NodeStateFresh, 60*time.Second),
		"term_receiver must reach fresh end to end once its terminal gate drains "+
			"(drain/dedupe unchanged by the broadened topic_kind discriminator)")
}

// waitForUndrainedReceiverWaitSetRow polls rimsky_wait_set for an
// UNDRAINED row gating one of the receiver's runs under the given
// topic_kind, returning (frame_id, receiver_run_id, sender_run_id) of
// the first such row. Reading an undrained row proves the receiver is
// genuinely mid-gate (the story's "while the receiver is gated"). The
// returned run/frame ids are reused as the real keys for the message-row
// insert below.
func waitForUndrainedReceiverWaitSetRow(
	t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID,
	topicKind string, timeout time.Duration,
) (frameID, receiverRunID, senderRunID shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var (
			fid shared.UUID
			rid shared.UUID
			sid shared.UUID
			ok  bool
		)
		h.QuerySQL(`
            SELECT w.frame_id, w.receiver_run_id, w.sender_run_id
              FROM rimsky_wait_set w
              JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
             WHERE r.node_id = $1
               AND w.topic_kind = $2
               AND w.drained_at IS NULL
             LIMIT 1
        `, []any{receiverNodeID, topicKind}, func(scan func(...any) error) error {
			if err := scan(&fid, &rid, &sid); err != nil {
				return err
			}
			ok = true
			return nil
		})
		if ok {
			return fid, rid, sid
		}
		time.Sleep(50 * time.Millisecond)
	}
	return shared.UUID{}, shared.UUID{}, shared.UUID{}
}

// waitForReceiverWaitSetTopicKind polls rimsky_wait_set (via the real
// InTx persistence read) for ANY row (drained or not) gating one of the
// receiver's runs under the given topic_kind. Drained rows remain
// queryable, so this is timing-robust.
func waitForReceiverWaitSetTopicKind(
	t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID,
	topicKind string, timeout time.Duration,
) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if receiverWaitSetHasTopicKind(t, h, receiverNodeID, topicKind) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// receiverWaitSetHasTopicKind reads the wait-set ledger through the real
// persistence surface (InTx → ListForFrame, the same accessor
// /admin/diagnostics/wait-sets uses) and reports whether any row gating
// one of the receiver's runs carries the given topic_kind.
func receiverWaitSetHasTopicKind(
	t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID, topicKind string,
) bool {
	t.Helper()
	for _, k := range distinctReceiverWaitSetTopicKinds(t, h, receiverNodeID) {
		if k == topicKind {
			return true
		}
	}
	return false
}

// distinctReceiverWaitSetTopicKinds reads, through the real InTx
// persistence surface, every wait-set row gating one of the receiver
// node's runs and returns the distinct topic_kind values observed across
// all of them (across frames; drained rows included). This is the
// node/diagnostic snapshot surface the gate names — it reads the ledger
// via the runtime's own WaitSetTable.ListForFrame accessor (the same one
// /admin/diagnostics/wait-sets uses), not by reaching past it.
func distinctReceiverWaitSetTopicKinds(
	t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID,
) []string {
	t.Helper()
	runs := receiverRunIDs(t, h, receiverNodeID)
	seen := map[string]struct{}{}
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		for _, run := range runs {
			rows, err := h.Persist.WaitSet().ListForFrame(h.Ctx, run.frameID, tx)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.ReceiverRunID == run.runID {
					seen[row.TopicKind] = struct{}{}
				}
			}
		}
		return nil
	}))
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

type receiverRun struct {
	runID   shared.UUID
	frameID shared.UUID
}

// receiverRunIDs returns every node-run (id + frame) for the receiver
// node across all frames. The WaitSet accessors are frame-scoped, so we
// enumerate the receiver's runs to know which frames to read. The
// persistence interface has no "runs by node-id across all frames"
// accessor, so this reads rimsky_node_runs directly; the ids it yields
// feed the real WaitSet().ListForFrame reads above.
func receiverRunIDs(t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID) []receiverRun {
	t.Helper()
	var runs []receiverRun
	h.QuerySQL(`
        SELECT id, frame_id FROM rimsky_node_runs WHERE node_id = $1
    `, []any{receiverNodeID}, func(scan func(...any) error) error {
		var r receiverRun
		if err := scan(&r.runID, &r.frameID); err != nil {
			return err
		}
		runs = append(runs, r)
		return nil
	})
	return runs
}
