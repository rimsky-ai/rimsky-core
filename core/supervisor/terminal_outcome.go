// Spec §12.6 — supervisor-side action mapping for terminal events.
//
// The legacy ApplyTerminalOutcome / Commit / OnError dispatch surface is
// gone: the omnibus runner inlined the full terminal-handling flow into
// runner_terminal.go (Task 27). What remains here is the conceptual
// content of §12.6 — the mapping from a terminal event (Complete /
// Blocked / Errored, plus the resolved policy-chain action for the
// non-Complete path) to the store.ReleaseAction the per-lock release
// transaction should apply.
//
// This file is intentionally small and dependency-light: it exports the
// mapping as a pure function so both the synchronous-runner path
// (runner_terminal.go) and any future async-callback applier can share
// the same table. Adding the dispatch / state-update / event-append
// side-effects here would re-create the legacy entry point we just
// retired; those side-effects belong to whoever owns the per-run
// transaction (today: runner_terminal.go).
package supervisor

import "github.com/fallguy/rimsky/core/store"

// TerminalKind classifies a node-run's terminal event per spec §12.2.
// Mirrors the runner-internal terminalKind but exported so external
// callers (callback paths, tests) can reason about the §12.6 table
// without reaching into runner internals.
type TerminalKind int

const (
	// TerminalKindComplete corresponds to spec §12.2 Complete.
	TerminalKindComplete TerminalKind = iota + 1
	// TerminalKindBlocked corresponds to spec §12.2 Blocked.
	TerminalKindBlocked
	// TerminalKindErrored corresponds to spec §12.2 Errored.
	TerminalKindErrored
	// TerminalKindInfra is an infra-layer error (silence_timeout,
	// supervisor_crash, executor_dial_failed, etc.). Spec §7.2: these
	// are restart events, not application retries; the §12.6 row maps
	// them to a give_up release so the lock-holder is dropped before
	// the orphan-reap or runner-side re-enqueue picks the node up
	// again.
	TerminalKindInfra
)

// PolicyResolution names the post-policy-chain action emitted by
// node.Evaluate for a Blocked / Errored terminal. Required by the
// §12.6 mapping for non-Complete terminals.
//
// The string values match the ones node.Evaluate already produces;
// they're echoed here as typed constants so the mapper's switch is
// exhaustive at the type level.
type PolicyResolution string

const (
	// PolicyDiscardThenRetry — release with give_up, then re-enqueue.
	PolicyDiscardThenRetry PolicyResolution = "discard_then_retry"
	// PolicyResumeThenRetry — release with preserve_for_resume, then
	// re-enqueue. Falls back to give_up if no spec is resumable.
	PolicyResumeThenRetry PolicyResolution = "resume_then_retry"
	// PolicyGiveUp — release with give_up; node→failed.
	PolicyGiveUp PolicyResolution = "give_up"
	// PolicyInvalidate — release with give_up; node→stale plus
	// invalidate(targets) cascade.
	PolicyInvalidate PolicyResolution = "invalidate"
	// PolicyRetry is the legacy back-compat policy emitted by
	// pre-redesign templates. Treated as resume-when-resumable, else
	// give_up — the same fallback the runner-internal mapper uses.
	PolicyRetry PolicyResolution = "retry"
)

// MapTerminalToReleaseAction implements the §12.6 mapping table. Pure;
// no I/O. The caller is responsible for any policy-chain evaluation
// (for Blocked / Errored) and for the resumable-spec check that gates
// preserve_for_resume.
//
// Mapping per spec §12.6:
//
//	Complete{changed: true}                         → ReleaseCommit
//	Complete{changed: false}                        → ReleaseCommit
//	Blocked|Errored + discard_then_retry            → ReleaseGiveUp
//	Blocked|Errored + resume_then_retry, resumable  → ReleasePreserveResume
//	Blocked|Errored + resume_then_retry, !resumable → ReleaseGiveUp (fallback)
//	Blocked|Errored + give_up                       → ReleaseGiveUp
//	Errored + invalidate(targets)                   → ReleaseGiveUp
//	Blocked|Errored + retry (legacy), resumable     → ReleasePreserveResume
//	Blocked|Errored + retry (legacy), !resumable    → ReleaseGiveUp
//	Infra (any kind)                                → ReleaseGiveUp
//
// `policyHasResumableSpec` is the acquisition's resumable-flag check;
// only consulted on resume_then_retry / retry. Pass false when no
// policy resolution applies (Complete, Infra).
func MapTerminalToReleaseAction(
	kind TerminalKind, policy PolicyResolution, policyHasResumableSpec bool,
) store.ReleaseAction {
	if kind == TerminalKindComplete {
		// Both changed=true and changed=false commit per §12.6 row 1+2.
		// `changed` only affects whether attributes get validated /
		// cascaded; the lock release action itself does not branch on
		// it.
		return store.ReleaseCommit
	}
	if kind == TerminalKindInfra {
		return store.ReleaseGiveUp
	}
	// Blocked / Errored — mapping driven by the resolved policy.
	switch policy {
	case PolicyResumeThenRetry, PolicyRetry:
		if policyHasResumableSpec {
			return store.ReleasePreserveResume
		}
		return store.ReleaseGiveUp
	case PolicyDiscardThenRetry, PolicyGiveUp, PolicyInvalidate:
		return store.ReleaseGiveUp
	}
	// Unknown / empty policy — default to give_up so the lock-holder
	// is always released.
	return store.ReleaseGiveUp
}
