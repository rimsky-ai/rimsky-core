// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// wait.go — terminal-wait loop for `rimsky compose run`. Polls each
// instance via GET /instances/{id} and its nodes via
// GET /instances/{id}/nodes until every declared instance has reached
// terminal (`terminated_at` set), then returns the per-instance
// outcome map. The verb's main select loop wraps this in a goroutine
// alongside signal handling and role-failure channels.
//
// @decision: termination, instance-self-termination
// @story: one-shot-to-terminal
package compose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// DefaultWaitPollInterval is the starting cadence
// WaitForInstancesTerminal uses when the caller passes 0 for
// pollInterval. Each poll fires two GET requests per still-running
// instance (GetInstance + nodes-list); each request triggers an
// auth.access_attempted audit-row write on the control-api side. The
// 1s cadence is the empirically tuned balance between live operator
// feel (per-node terminal events surface within a second of the
// control-api recording them) and not starving the scheduler /
// supervisor / instance-terminator background workers competing for
// the same persistence layer's write path. The existing
// `rimsky run --no-keep` poll is also 1s.
//
// @decision: progress-default — the operator-observable live cadence
// is bounded by this interval.
const DefaultWaitPollInterval = 1 * time.Second

// maxWaitPollInterval is the back-off ceiling. The wait loop polls at
// the requested cadence for the first waitPollBackoffAfter ticks and
// then doubles the interval up to this ceiling. Long runs spend the
// bulk of their duration at this slower cadence — the operator's
// situational-awareness window stays bounded (≤5s between observable
// transitions), the audit-row pressure on the in-process sqlite
// driver drops by 5×, and the supervisor's claim-loop and dispatch
// transactions are no longer competing with the wait loop for the
// sqlite writer slot at the original cadence.
//
// @decision: progress-default
const maxWaitPollInterval = 5 * time.Second

// waitPollBackoffAfter is the number of ticks at the requested
// cadence before the back-off starts. Keeps the operator-observable
// live feel of a fresh run intact for the first few ticks (where
// most lifecycle transitions land) before falling back to the slower
// cadence for the steady state.
const waitPollBackoffAfter = 5

// @decision: exit-codes — instance outcome strings shared between the
// wait loop and the verb's exit-code classification. The control-api
// returns terminal nodes with `state` in {"success", "failed",
// "parked"} (the last meaning the node parked itself with a timeout);
// the verb maps "failed" to failure and "parked" to parked-timeout
// for the operator-facing summary.
const (
	OutcomeSuccess       = "success"
	OutcomeFailure       = "failure"
	OutcomeParkedTimeout = "parked-timeout"
)

// instanceClient is the slice of cli.Client the wait loop calls into.
// Defining it as an interface lets the tests substitute a fake without
// spinning up a full httptest server for every poll-loop scenario; the
// real cli.Client satisfies it because both methods exist on its
// concrete type.
type instanceClient interface {
	GetInstance(ctx context.Context, idOrKey string) (*cli.Instance, error)
	ListInstanceNodes(ctx context.Context, idOrKey string) (*cli.ListInstanceNodesResponse, error)
}

// WaitForInstancesTerminal polls each instance ID until every one
// reaches terminal (`terminated_at` non-nil) and returns the per-id
// outcome map. The printer is called as each new node-run terminal
// is observed and as each instance flips to terminal. The function
// returns ctx.Err() when the supplied context is cancelled (the
// caller's signal-handling path interrupts the wait this way).
//
// pollInterval == 0 → DefaultWaitPollInterval. The instanceIDs slice
// is treated as the authoritative roster: an empty roster returns an
// empty map with nil error (a manifest with no instances is a
// degenerate but legal input, and the verb should exit cleanly).
//
// @agent-contract guarantees: returns nil error only after every id
// in instanceIDs has been observed with terminated_at non-nil; the
// per-id outcomes map carries one entry per id, valued
// OutcomeSuccess | OutcomeFailure | OutcomeParkedTimeout. Returns
// ctx.Err() on context cancellation (partial outcomes still
// populated where observed). Does NOT delete or terminate instances;
// the verb's @decision: instance-self-termination path sets
// terminate_after_run=true on CreateInstance, which is what makes
// terminated_at flip from the control-api side.
func WaitForInstancesTerminal(
	ctx context.Context,
	client instanceClient,
	instanceIDs []string,
	project string,
	keys map[string]string,
	printer ProgressPrinter,
	pollInterval time.Duration,
) (map[string]string, error) {
	if pollInterval <= 0 {
		pollInterval = DefaultWaitPollInterval
	}
	// @constraint: the printer must be non-nil. The production callsite
	// always passes one (the flag-resolved printer) and the tests
	// supply nopPrinter when they need a silent sink. Failing fast here
	// keeps a nil-printer wiring bug from surfacing later as an opaque
	// nil-receiver panic deep in the loop.
	if printer == nil {
		panic("compose: WaitForInstancesTerminal: printer must be non-nil")
	}
	outcomes := make(map[string]string, len(instanceIDs))
	if len(instanceIDs) == 0 {
		return outcomes, nil
	}

	// @constraint: dedupe printer events for terminal nodes across
	// repeat polls — the poll lands again after terminated_at flips
	// and would otherwise re-emit the same NodeRunTerminal event.
	seenNodeTerminal := make(map[string]bool)

	remaining := make(map[string]bool, len(instanceIDs))
	for _, id := range instanceIDs {
		remaining[id] = true
		if name := keys[id]; name != "" {
			printer.InstanceStarting(project, name)
		} else {
			printer.InstanceStarting(project, id)
		}
	}

	// @deliberate: Timer (not Ticker) so the back-off can change the
	// period each tick without dropping pending fires; the first
	// waitPollBackoffAfter ticks run at the requested cadence, then
	// the interval doubles each tick up to maxWaitPollInterval.
	currentInterval := pollInterval
	timer := time.NewTimer(currentInterval)
	defer timer.Stop()
	tickCount := 0

	for len(remaining) > 0 {
		// @constraint: per-id polls are sequential within a tick but
		// errors on one id must not stall the others — a transient
		// blip on one instance falls through `continue` and retries
		// on the next tick instead of aborting the wait.
		for id := range remaining {
			inst, err := client.GetInstance(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					return outcomes, ctx.Err()
				}
				// @constraint: a non-cancel error is best-effort
				// degraded — the caller's role-failure channel will
				// surface a hard backend failure separately. Drop
				// this attempt and try again on the next tick.
				continue
			}

			nodes, nerr := client.ListInstanceNodes(ctx, id)
			if nerr == nil && nodes != nil {
				name := keys[id]
				if name == "" {
					name = id
				}
				for _, n := range nodes.Nodes {
					if !isNodeTerminal(n.State) {
						continue
					}
					if seenNodeTerminal[n.ID] {
						continue
					}
					seenNodeTerminal[n.ID] = true
					nodeOutcome, reason := mapNodeStateToOutcome(n)
					printer.NodeRunTerminal(project, name, n.ID, nodeOutcome, reason)
				}
			} else if nerr != nil && ctx.Err() != nil {
				return outcomes, ctx.Err()
			}

			if inst.TerminatedAt == nil {
				continue
			}
			// @constraint: instance is terminal but the per-tick
			// ListInstanceNodes failed transiently — we have no
			// node roster to classify, so DO NOT promote to success
			// (the OutcomeSuccess default is for a node roster we
			// looked at and saw no failures). Skip this id this
			// tick; the next tick re-fetches the nodes and either
			// classifies it correctly or hits the same condition
			// again. The control-api will keep returning the same
			// terminated_at on every subsequent poll, so the only
			// cost of waiting is one tick. Misclassifying a failed
			// instance as success breaks the script-friendly-outcome
			// distinct-exit-code contract that STORY-script-friendly-
			// outcome names; leaving it `remaining` does not.
			if nodes == nil {
				continue
			}
			name := keys[id]
			if name == "" {
				name = id
			}
			outcome, frames := classifyInstanceOutcome(nodes.Nodes)
			outcomes[id] = outcome
			printer.InstanceTerminal(project, name, outcome, frames)
			delete(remaining, id)
		}
		if len(remaining) == 0 {
			break
		}

		select {
		case <-ctx.Done():
			return outcomes, ctx.Err()
		case <-timer.C:
		}

		// @decision: progress-default — back off after the warm-up
		// window so a long-running wait stops hammering the local
		// control-api (and the audit-row writer) at the original
		// cadence.
		tickCount++
		if tickCount >= waitPollBackoffAfter && currentInterval < maxWaitPollInterval {
			currentInterval *= 2
			if currentInterval > maxWaitPollInterval {
				currentInterval = maxWaitPollInterval
			}
		}
		timer.Reset(currentInterval)
	}
	return outcomes, nil
}

// classifyInstanceOutcome walks the node list and reports the
// dominant outcome for the instance plus a coarse frame count. The
// priority is failure > parked-timeout > success: one failed node
// fails the instance for exit-code purposes, regardless of how many
// other nodes succeeded. Frame count is the number of nodes observed
// — the precise per-frame ticks live in the verbose printer path;
// the summary frame count is enough for the script-friendly outcome
// story.
func classifyInstanceOutcome(nodes []cli.Node) (string, int) {
	hasFailure := false
	hasParked := false
	for _, n := range nodes {
		switch n.State {
		case "failed":
			hasFailure = true
		case "parked":
			hasParked = true
		}
	}
	switch {
	case hasFailure:
		return OutcomeFailure, len(nodes)
	case hasParked:
		return OutcomeParkedTimeout, len(nodes)
	default:
		return OutcomeSuccess, len(nodes)
	}
}

// mapNodeStateToOutcome maps a single terminal node's wire state to
// the operator-facing outcome label, plus an optional reason note
// surfaced on a non-success outcome (currently the node's
// CurrentErrorClass).
func mapNodeStateToOutcome(n cli.Node) (string, string) {
	switch n.State {
	case "failed":
		return OutcomeFailure, n.CurrentErrorClass
	case "parked":
		return OutcomeParkedTimeout, n.CurrentErrorClass
	default:
		return OutcomeSuccess, ""
	}
}

func isNodeTerminal(state string) bool {
	switch state {
	case "success", "failed", "parked":
		return true
	default:
		return false
	}
}

// AnyOutcomeFailed reports whether the per-instance outcomes contain
// a failure or parked-timeout entry. The verb's main loop uses this
// to map the wait result to ReasonAnyFailure / ReasonAllSuccess in
// the @decision: exit-codes table.
func AnyOutcomeFailed(outcomes map[string]string) bool {
	for _, o := range outcomes {
		if o != OutcomeSuccess {
			return true
		}
	}
	return false
}

// classifyWaitErr maps the wait-loop error to a ShutdownReason for
// the verb's drain. ctx.Canceled means the caller cancelled
// (signal); ctx.DeadlineExceeded means the timeout fired. Any other
// error is the conservative ReasonAnyFailure (treat unknown
// upstream errors as failure, not success).
func classifyWaitErr(err error) ShutdownReason {
	if err == nil {
		return ReasonAllSuccess
	}
	switch {
	case errors.Is(err, context.Canceled):
		return ReasonSignal
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout
	default:
		return ReasonAnyFailure
	}
}

// reasonString keeps the fmt-Stringer impl close to the constants
// it formats so a debug log of the reason is human-legible.
func reasonString(r ShutdownReason) string {
	switch r {
	case ReasonAllSuccess:
		return "all-success"
	case ReasonAnyFailure:
		return "any-failure"
	case ReasonTimeout:
		return "timeout"
	case ReasonSignal:
		return "signal"
	default:
		return fmt.Sprintf("unknown-reason-%d", int(r))
	}
}
