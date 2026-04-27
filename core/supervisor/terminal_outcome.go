// Spec §12.6 — supervisor-side terminal action vocabulary.
//
// Under stores-redesign-v2 the legacy ReleaseAction enum is gone:
// release routing happens per-claim via `claim_resolutions` (§14.3)
// and the auto-terminal §14.4.1 routing table. What remains here is
// the conceptual classification of terminal events and the
// policy-resolution names that flow between the executor stream
// classifier and the supervisor's runner_terminal.go.

package supervisor

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
	// TerminalKindInfra is an infra-layer error.
	TerminalKindInfra
)

// PolicyResolution names the post-policy-chain action emitted by
// node.Evaluate for a Blocked / Errored terminal. The string values
// match what node.Evaluate produces.
type PolicyResolution string

const (
	// PolicyDiscardThenRetry — release with failure-branch verb,
	// then re-enqueue.
	PolicyDiscardThenRetry PolicyResolution = "discard_then_retry"
	// PolicyResumeThenRetry — preserved for back-compat with templates
	// that still reference it; under v2 there is no preserve-for-resume
	// release path (resume is universal at the substrate). The
	// supervisor treats it as an alias of discard_then_retry.
	PolicyResumeThenRetry PolicyResolution = "resume_then_retry"
	// PolicyGiveUp — release with failure-branch verb; node→failed.
	PolicyGiveUp PolicyResolution = "give_up"
	// PolicyInvalidate — release with failure-branch verb; node→stale
	// plus invalidate(targets) cascade.
	PolicyInvalidate PolicyResolution = "invalidate"
	// PolicyRetry is the legacy back-compat policy emitted by
	// pre-redesign templates.
	PolicyRetry PolicyResolution = "retry"
)
