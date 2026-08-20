// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type NodeRunSummary struct {
	ActiveCount  int `json:"active_count"`
	PendingCount int `json:"pending_count"`
	FreshCount   int `json:"fresh_count"`
	FailedCount  int `json:"failed_count"`
}

type NodeEvent struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

type NodeObservability struct {
	NodeType         string          `json:"node_type"`
	RunSummary       NodeRunSummary  `json:"run_summary"`
	Events           []NodeEvent     `json:"events"`
	LatestAttributes json.RawMessage `json:"latest_attributes"`
}

func (o NodeObservability) HasEventKind(kind string) bool {
	for _, e := range o.Events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func (e RimskyEndpoint) GetNodeObservability(t testing.TB, instanceID, nodeType string) (int, NodeObservability, []byte) {
	t.Helper()
	status, raw := e.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
	var obs NodeObservability
	if status == http.StatusOK {
		if err := json.Unmarshal(raw, &obs); err != nil {
			t.Fatalf("harness: decode node observability %s/%s: %v: %s", instanceID, nodeType, err, string(raw))
		}
	}
	return status, obs, raw
}

// @decision: testing-scenario-based-e2e
func (e RimskyEndpoint) PollNodeObservability(
	t testing.TB,
	instanceID, nodeType string,
	until func(NodeObservability) bool,
) NodeObservability {
	t.Helper()
	for poll := 1; ; poll++ {
		var obs NodeObservability
		status, raw, err := e.TryGetJSON("/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if err == nil && status == http.StatusOK {
			if decErr := json.Unmarshal(raw, &obs); decErr != nil {
				t.Fatalf("harness: decode node observability %s/%s: %v: %s", instanceID, nodeType, decErr, string(raw))
			}
			if until(obs) {
				return obs
			}
		}
		if poll == 1 {
			t.Logf("harness: polling node %s/%s for the awaited state (first observation: status=%d, err=%v, run_summary=%+v) — "+
				"the helper blocks until the state appears and says so once, so a wait that never ends leaves the test "+
				"guard's no-progress watchdog free to trip",
				instanceID, nodeType, status, err, obs.RunSummary)
		}
		//nolint:testwallclock-outcome inter-poll cadence; this loop returns only when the awaited state appears
		time.Sleep(250 * time.Millisecond)
	}
}

func (e RimskyEndpoint) WaitForNodeTerminal(t testing.TB, instanceID, nodeType string) NodeObservability {
	t.Helper()
	return e.PollNodeObservability(t, instanceID, nodeType, func(o NodeObservability) bool {
		if o.RunSummary.FailedCount > 0 {
			return true
		}
		return o.RunSummary.FreshCount > 0 && o.RunSummary.ActiveCount == 0 && o.RunSummary.PendingCount == 0
	})
}

func (e RimskyEndpoint) RequireNodeTerminalSucceeded(t testing.TB, instanceID, nodeType string) NodeObservability {
	t.Helper()
	obs := e.WaitForNodeTerminal(t, instanceID, nodeType)
	if obs.RunSummary.FailedCount > 0 {
		t.Fatalf("harness: node %s/%s reached terminal with failed_count=%d (want 0, run to succeed); run_summary=%+v",
			instanceID, nodeType, obs.RunSummary.FailedCount, obs.RunSummary)
	}
	return obs
}

func (e RimskyEndpoint) WaitForNodeSettledTo(t testing.TB, instanceID, nodeType, want string) NodeObservability {
	t.Helper()
	var failedEarly bool
	obs := e.PollNodeObservability(t, instanceID, nodeType, func(o NodeObservability) bool {
		switch want {
		case "fresh":
			if o.RunSummary.FreshCount > 0 && o.RunSummary.ActiveCount == 0 && o.RunSummary.PendingCount == 0 {
				return true
			}
			if o.RunSummary.FailedCount > 0 {
				failedEarly = true
				return true
			}
		case "failed":
			return o.RunSummary.FailedCount > 0
		}
		return false
	})
	if failedEarly {
		t.Fatalf("harness: node %q on instance %s settled with failed_count=%d, want fresh terminal (run_summary=%+v)",
			nodeType, instanceID, obs.RunSummary.FailedCount, obs.RunSummary)
	}
	return obs
}
