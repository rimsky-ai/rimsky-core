// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// The one standard path for emitting a node-run's run-disposition signal.
//
// A node-run signal that cascade-fires subscribers — terminal/success,
// terminal/error/<class>, and transient/retry/<n>/<class> — is emitted
// through emitSignalInTx, which fires the subscription cascade AND lands
// the audit row as one atomic in-tx operation.
//
// Why this file exists: before it, "fire the cascade" and "write the
// audit row" were two separate calls hand-paired at each settlement site
// (~7 cascade sites, ~13 emit sites). A site that wrote the audit row but
// forgot the cascade emitted a signal no subscriber ever saw — the exact
// bug behind a node that subscribed to an upstream's terminal/error/*
// never firing. Collapsing the pair into one call makes that class of bug
// unrepresentable: you cannot emit a cascade-firing disposition without
// cascading it.
//
// What is deliberately NOT routed through here (and why):
//   - terminal/park/<reason> (applyTerminalPark) and terminal/infra/<class>
//     (applyTerminalInfraError) are NOT settlements — the node is
//     suspended or re-enqueued and resumes, so a `terminal/*` subscriber
//     ("react when the upstream is DONE") must NOT fire on them. They emit
//     a BARE audit row, no cascade. (See the comment in applyTerminalPark
//     and TestParkedLifecycleHeldClaimRetentionAcrossPark for the
//     held-claim breakage a park cascade causes.)
//   - attribute/<key>/changed are data signals: cascaded at terminal
//     but audited on their own schedule (see applyTerminalComplete).
//     Per TD-collapse-named-event-to-tags the historic `event/<name>`
//     data signal retired — non-terminal observable transitions ride
//     as tags on the settling terminal verdict (concept:terminal-tag).
//   - message/* uses a distinct subscriber edge map (message_delivery.go).
package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
)

// emitSignalInTx is THE single path by which a node-run's run-disposition
// signal both drives the subscription cascade and lands its canonical
// audit row — atomically, inside the caller's transaction.
//
// @agent-contract: one standard path for cascade-firing disposition signals.
//
//   - What: every node-run signal that should cascade-fire subscribers —
//     terminal/success, terminal/error/<class> (settlements), and
//     transient/retry/<n>/<class> (retry gating) — is emitted by calling
//     this, never by calling a cascade helper and signalaudit.EmitSignal
//     separately. Non-cascading dispositions (terminal/park, terminal/infra)
//     are listed in the file header and stay bare audits by design.
//
//   - How to use: pass the sender node/run/frame and the signal. The
//     cascade runs first (affirms receivers + inserts wait-set rows),
//     then the audit row lands; both co-commit in the caller's tx, so an
//     audit row can never contradict the cascade it drove. For a node
//     that emits several signals at one terminal (terminal/success plus
//     per-key attribute/<key>/changed), thread one shared `visited` map
//     so each receiver is affirmed at most once across the set.
//
//   - Audit always; cascade when able. The cascade needs a real run +
//     frame to gate receivers on. In the defensive settlement edge where
//     the run has already been retired (zero senderRunID or zero
//     senderFrameID), the cascade is skipped but the audit row STILL
//     lands — a disposition signal is never silently dropped. Callers may
//     therefore emit UNCONDITIONALLY (a resolution always audits; it
//     cascades whenever there is something to cascade to).
//
//   - What it handles: the cascade fire and the audit write, together,
//     for ANY signal. It is signal-blind — it never inspects sig.Type,
//     and neither does the cascade engine beneath it
//     (cascadeSubscribersStaleInTxWithVisited). Which subscribers fire is
//     decided purely by subscription-edge match + CEL when: predicate.
//     Do NOT add signal-type branching here or below: subscriptions
//     drive cascades, period.
//
//   - What it does NOT handle: data signal attribute/<key>/changed
//     (cascaded at terminal but audited on its own schedule — see
//     applyTerminalComplete) and message/* (a distinct subscriber
//     edge map, see message_delivery.go). The pre-Pass-1
//     `event/<name>` data signal is retired entirely under
//     TD-collapse-named-event-to-tags.
//
//   - Thread-safety: none of its own; it runs inside the caller's tx and
//     inherits that tx's isolation.
//
//     @concept: cascade
//     @concept: signal
//     @concept: wait-set
func emitSignalInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
	visited map[foundationshared.UUID]struct{},
) error {
	// @constraint: cascade only when there is a real run + frame to gate
	// receivers on. A zero senderRunID/senderFrameID is the defensive
	// settlement edge (the run was already retired by a concurrent sweep):
	// skip the cascade, but still write the audit row below so the
	// disposition is never lost.
	var zeroUUID foundationshared.UUID
	if senderRunID != zeroUUID && senderFrameID != zeroUUID {
		if err := cascadeSubscribersStaleInTxWithVisited(ctx, args, tx,
			senderID, senderNodeType, senderRunID, instanceID, senderFrameID, sig, visited); err != nil {
			return err
		}
	}
	return signalaudit.EmitSignal(ctx, args.Persist.Events(),
		instanceID, senderID, sig, args.Clock.Now(), tx)
}

// emitSignalInTxOnce is the single-signal form of emitSignalInTx: a node
// emitting exactly one cascade-firing disposition signal (give_up / pass
// terminal/error, or a transient/retry) calls this. It allocates a fresh
// once-per-frame receiver guard, since there is only one signal in the set.
func emitSignalInTxOnce(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
) error {
	return emitSignalInTx(ctx, args, tx, senderID, senderNodeType, senderRunID,
		instanceID, senderFrameID, sig, map[foundationshared.UUID]struct{}{})
}
