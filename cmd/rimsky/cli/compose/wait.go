// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: termination
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
	OutcomeSuccess = cli.OutcomeSuccess
	OutcomeFailure = cli.OutcomeFailure
)

type instanceClient interface {
	ListInstanceNodes(ctx context.Context, idOrKey string) (*cli.ListInstanceNodesResponse, error)
	ListInstanceFrames(ctx context.Context, idOrKey, state string) (*cli.ListFramesResponse, error)
	ListInstanceMessages(ctx context.Context, idOrKey string, q cli.ListMessagesQuery) (*cli.ListMessagesResponse, error)
	TerminateInstance(ctx context.Context, idOrKey string, reason string) (*cli.Instance, error)
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
	frames := newFrameTicker()

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
			name := keys[id]
			if name == "" {
				name = id
			}
			nodes, nerr := client.ListInstanceNodes(ctx, id)
			if nerr == nil && nodes != nil {
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

			idle, running, ierr := instanceIsIdle(ctx, client, id)
			if ierr != nil {
				if ctx.Err() != nil {
					return outcomes, ctx.Err()
				}
				continue
			}
			for _, f := range frames.observe(id, running) {
				printer.FrameTick(project, name, f.id, f.ordinal)
			}
			if nodes == nil || !idle {
				continue
			}
			outcome, nodeCount := classifyInstanceOutcome(nodes.Nodes)
			outcomes[id] = outcome
			printer.InstanceTerminal(project, name, outcome, nodeCount)
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

		tickCount++
		currentInterval = nextWaitPollInterval(tickCount, currentInterval)
		timer.Reset(currentInterval)
	}
	return outcomes, nil
}

// @decision: progress-default
func nextWaitPollInterval(tickCount int, current time.Duration) time.Duration {
	if tickCount < waitPollBackoffAfter || current >= maxWaitPollInterval {
		return current
	}
	current *= 2
	if current > maxWaitPollInterval {
		current = maxWaitPollInterval
	}
	return current
}

// @concept: frame
type observedFrame struct {
	id      string
	ordinal int
}

type frameTicker struct {
	seen  map[string]map[string]bool
	count map[string]int
}

func newFrameTicker() *frameTicker {
	return &frameTicker{seen: map[string]map[string]bool{}, count: map[string]int{}}
}

func (ft *frameTicker) observe(instanceID string, running []cli.FrameItem) []observedFrame {
	var fresh []observedFrame
	for _, f := range running {
		if ft.seen[instanceID][f.FrameID] {
			continue
		}
		if ft.seen[instanceID] == nil {
			ft.seen[instanceID] = map[string]bool{}
		}
		ft.seen[instanceID][f.FrameID] = true
		ft.count[instanceID]++
		fresh = append(fresh, observedFrame{id: f.FrameID, ordinal: ft.count[instanceID]})
	}
	return fresh
}

// @story: one-shot-to-terminal
// @concept: frame
func instanceIsIdle(ctx context.Context, client instanceClient, id string) (bool, []cli.FrameItem, error) {
	frames, err := client.ListInstanceFrames(ctx, id, "running")
	if err != nil {
		return false, nil, err
	}
	if len(frames.Frames) > 0 {
		return false, frames.Frames, nil
	}
	pending := true
	messages, err := client.ListInstanceMessages(ctx, id, cli.ListMessagesQuery{Pending: &pending})
	if err != nil {
		return false, nil, err
	}
	return len(messages.Messages) == 0, nil, nil
}

// @concept: node-run
func classifyInstanceOutcome(nodes []cli.Node) (string, int) {
	return cli.ClassifyInstanceOutcome(nodes)
}

// @concept: node-run
func mapNodeSummaryToOutcome(n cli.Node) (string, string) {
	if n.RunSummary != nil && n.RunSummary.FailedCount > 0 {
		return OutcomeFailure, n.SettlingSignalType
	}
	return OutcomeSuccess, n.SettlingSignalType
}

// @concept: node-run
func isNodeSettled(s *cli.NodeRunSummary) bool {
	if s == nil {
		return false
	}
	if s.ActiveCount > 0 || s.PendingCount > 0 {
		return false
	}
	return s.FreshCount > 0 || s.FailedCount > 0
}

// @concept: node-run
func allNodesSettled(nodes []cli.Node) bool {
	for _, n := range nodes {
		if !isNodeSettled(n.RunSummary) {
			return false
		}
	}
	return true
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

func bootFailureReason(bootCtx context.Context) ShutdownReason {
	if bootCtx.Err() != nil {
		return ReasonSignal
	}
	return ReasonAnyFailure
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
