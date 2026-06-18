// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

// @decision: progress-default — the operator-observable live cadence
const DefaultWaitPollInterval = 1 * time.Second

// @decision: progress-default
const maxWaitPollInterval = 5 * time.Second

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

type instanceClient interface {
	GetInstance(ctx context.Context, idOrKey string) (*cli.Instance, error)
	ListInstanceNodes(ctx context.Context, idOrKey string) (*cli.ListInstanceNodesResponse, error)
}

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
	if printer == nil {
		panic("compose: WaitForInstancesTerminal: printer must be non-nil")
	}
	outcomes := make(map[string]string, len(instanceIDs))
	if len(instanceIDs) == 0 {
		return outcomes, nil
	}

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

	currentInterval := pollInterval
	timer := time.NewTimer(currentInterval)
	defer timer.Stop()
	tickCount := 0

	for len(remaining) > 0 {
		for id := range remaining {
			inst, err := client.GetInstance(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					return outcomes, ctx.Err()
				}
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

func AnyOutcomeFailed(outcomes map[string]string) bool {
	for _, o := range outcomes {
		if o != OutcomeSuccess {
			return true
		}
	}
	return false
}

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
