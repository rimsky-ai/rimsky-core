// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: termination
// @decision: instance-self-termination
// @story: one-shot-to-terminal
package compose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// @decision: progress-default
const DefaultWaitPollInterval = 1 * time.Second

// @decision: progress-default
const maxWaitPollInterval = 5 * time.Second

const waitPollBackoffAfter = 5

// @decision: exit-codes
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
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
					if !isNodeSettled(n.RunSummary) {
						continue
					}
					if seenNodeTerminal[n.ID] {
						continue
					}
					seenNodeTerminal[n.ID] = true
					nodeOutcome, reason := mapNodeSummaryToOutcome(n)
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

		// @decision: progress-default
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

// @concept: node
func classifyInstanceOutcome(nodes []cli.Node) (string, int) {
	for _, n := range nodes {
		if n.RunSummary != nil && n.RunSummary.FailedCount > 0 {
			return OutcomeFailure, len(nodes)
		}
	}
	return OutcomeSuccess, len(nodes)
}

// @concept: node
func mapNodeSummaryToOutcome(n cli.Node) (string, string) {
	if n.RunSummary != nil && n.RunSummary.FailedCount > 0 {
		return OutcomeFailure, n.SettlingSignalType
	}
	return OutcomeSuccess, n.SettlingSignalType
}

// @concept: node
func isNodeSettled(s *cli.NodeRunSummary) bool {
	if s == nil {
		return false
	}
	if s.ActiveCount > 0 || s.PendingCount > 0 {
		return false
	}
	return s.FreshCount > 0 || s.FailedCount > 0
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
