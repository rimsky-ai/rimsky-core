// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// ErrorTypePolicy and PolicyAction describe the per-error-class repair
// chains declared in a node's error_types template block.
//
// The Evaluate function that advances the chain lives in graph/node;
// this package defines only the persistable data shape.
type ErrorTypePolicy struct {
	Policy []PolicyAction `yaml:"policy" json:"policy"`
}

// PolicyAction is one entry in a node's per-error-class repair chain.
//
// Action vocabulary (carried forward through the 2026-04-30 stores
// cleanup; the rimsky-side store verb is the success/failure
// binary — success → Commit, failure → Abandon — and the store
// decides what those mean for its own state per its own configuration):
//   - "retry"              — generic retry; the runner releases the
//     claim by firing Abandon on the store before re-enqueue.
//   - "discard_then_retry" — explicitly request `Abandon` (staged
//     stores) or release-by-Abandon (pick-policy stores) before
//     re-enqueue. The v3 standard filesystem store is `direct`-only;
//     for direct stores Abandon is degenerate (writes cannot be
//     undone), so discard_then_retry is effectively keep-then-retry
//     on those stores.
//   - "resume_then_retry"  — historical action vocabulary preserved
//     for backwards-compatible policy declarations. Behaviorally an
//     alias for `discard_then_retry`: the runner releases each claim
//     by firing `Abandon` on the store before re-enqueue. Explicit
//     Release-routing for read-side state is not in scope for the
//     2026-04-30 stores cleanup; if a future cycle reintroduces it,
//     update both this comment and `applyResolvedAction` together.
//   - "invalidate"         — return targets; per-claim release fires
//     Abandon on the store.
//   - "give_up"            — terminal failure; per-claim release
//     fires Abandon on the store.
type PolicyAction struct {
	Action         string      `yaml:"action" json:"action"`
	Count          int         `yaml:"count,omitempty" json:"count,omitempty"`
	Backoff        BackoffKind `yaml:"backoff,omitempty" json:"backoff,omitempty"`
	Jitter         JitterKind  `yaml:"jitter,omitempty" json:"jitter,omitempty"`
	BaseDelayMs    int         `yaml:"base_delay_ms,omitempty" json:"base_delay_ms,omitempty"`
	MaxDelayMs     int         `yaml:"max_delay_ms,omitempty" json:"max_delay_ms,omitempty"`
	Targets        []string    `yaml:"targets,omitempty" json:"targets,omitempty"`
	ReasonTemplate string      `yaml:"reason_template,omitempty" json:"reason_template,omitempty"`
	// Frame controls whether the invalidate emit (when Action ==
	// "invalidate") joins the current cascade (FrameIn) or buffers
	// through frame.EnqueueOrCoalesce as a new frame (FrameNext;
	// default). See the reactive-loops + lifecycle-handlers spec §5.
	Frame string `yaml:"frame,omitempty" json:"frame,omitempty"`
}

// EvaluatorState is the persisted per-node, per-error-class policy chain
// position. Stored on the rimsky_nodes row as (current_error_class,
// retry_counter, action_index).
type EvaluatorState struct {
	ActionIndex       int
	RetryCounter      int
	CurrentErrorClass string
}

// ResolvedAction is the outcome of one Evaluate call. Kind carries the
// runtime intent the runner branches on:
//
//   - "retry"              — generic retry; runner fires Abandon on
//     the store before re-enqueue.
//   - "discard_then_retry" — retry with explicit Abandon (or Release
//     for stores with read-side state at Open).
//   - "resume_then_retry"  — alias for `discard_then_retry`; the
//     runner fires `Abandon` on each claim before re-enqueue. Kept as
//     a distinct kind so policy authors can express intent in
//     declarations; the runtime routing is identical to retry today.
//   - "invalidate"         — targets returned in Targets.
//   - "give_up"            — terminal.
type ResolvedAction struct {
	Kind    string
	DelayMs int
	Targets []string
	// Frame is propagated from PolicyAction.Frame for invalidate
	// actions; the runner forwards this through to InvalidateNode's
	// Frame field. Empty defaults to FrameNext at the call site.
	Frame    string
	Reason   string
	NewState EvaluatorState
}
