// Supervisor-side terminal action vocabulary.
//
// Under the 2026-04-30 stores cleanup the rimsky-side release routing
// is the success/failure binary: success → Commit; failure → Abandon.
// The store decides what those mean for its own state per its
// own configuration. What remains here is the conceptual
// classification of terminal events that flow between the executor
// stream classifier and the supervisor's runner_terminal.go.

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
	// that still reference it; under v3 there is no preserve-for-resume
	// release path (resume is universal at the store). The
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
