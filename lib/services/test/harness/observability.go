// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (e RimskyEndpoint) PollNodeObservability(
	t testing.TB,
	instanceID, nodeType string,
	deadline time.Duration,
	until func(NodeObservability) bool,
) (NodeObservability, bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last NodeObservability
	for time.Now().Before(end) {
		status, obs, _ := e.GetNodeObservability(t, instanceID, nodeType)
		if status == http.StatusOK {
			last = obs
			if until(obs) {
				return obs, true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return last, false
}

func (e RimskyEndpoint) WaitForNodeTerminal(t testing.TB, instanceID, nodeType string, deadline time.Duration) NodeObservability {
	t.Helper()
	obs, ok := e.PollNodeObservability(t, instanceID, nodeType, deadline, func(o NodeObservability) bool {
		if o.RunSummary.FailedCount > 0 {
			return true
		}
		return o.RunSummary.FreshCount > 0 && o.RunSummary.ActiveCount == 0 && o.RunSummary.PendingCount == 0
	})
	if !ok {
		t.Fatalf("harness: node %s/%s did not reach terminal within %v; last run_summary=%+v",
			instanceID, nodeType, deadline, obs.RunSummary)
	}
	return obs
}

func (e RimskyEndpoint) RequireNodeTerminalSucceeded(t testing.TB, instanceID, nodeType string, deadline time.Duration) NodeObservability {
	t.Helper()
	obs := e.WaitForNodeTerminal(t, instanceID, nodeType, deadline)
	if obs.RunSummary.FailedCount > 0 {
		t.Fatalf("harness: node %s/%s reached terminal with failed_count=%d (want 0, run to succeed); run_summary=%+v",
			instanceID, nodeType, obs.RunSummary.FailedCount, obs.RunSummary)
	}
	return obs
}

func (e RimskyEndpoint) WaitForNodeSettledTo(t testing.TB, instanceID, nodeType, want string, deadline time.Duration) NodeObservability {
	t.Helper()
	var failedEarly bool
	obs, ok := e.PollNodeObservability(t, instanceID, nodeType, deadline, func(o NodeObservability) bool {
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
	if !ok {
		t.Fatalf("harness: node %q on instance %s did not reach %q within %v; last run_summary=%+v",
			nodeType, instanceID, want, deadline, obs.RunSummary)
	}
	return obs
}
