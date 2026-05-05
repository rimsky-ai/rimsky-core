// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"context"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// CascadeNode is one node in the cascade-graph response. Per spec §1.4.
type CascadeNode struct {
	NodeType          string             `json:"node_type"`
	NodeID            shared.UUID        `json:"node_id"`
	State             shared.NodeState   `json:"state"`
	CurrentErrorClass string             `json:"current_error_class,omitempty"`
	RetryCounter      int                `json:"retry_counter"`
	ActiveDispatchID  *shared.UUID       `json:"active_dispatch_id,omitempty"`
	LastTerminalEvent *terminalEventView `json:"last_terminal_event,omitempty"`
	EdgesIn           []string           `json:"edges_in"`
	EdgesOut          []string           `json:"edges_out"`
}

type terminalEventView struct {
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurred_at"`
}

// computeCascadeGraph builds the per-instance cascade graph by joining
// the template spec's node declarations with the live rimsky_nodes
// rows for the instance. last_terminal_event is sourced from
// rimsky_events filtered by node_id and kind in the dispatch-terminal
// set, fetched in a single batch lookup to avoid the per-node N+1.
func computeCascadeGraph(ctx context.Context, deps Deps, _ persistence.InstanceRow, nodes []persistence.NodeRow, template *persistence.TemplateRow) []CascadeNode {
	// Index live nodes by node_type for O(1) projection.
	byType := make(map[string]persistence.NodeRow, len(nodes))
	nodeIDs := make([]shared.UUID, 0, len(nodes))
	for _, n := range nodes {
		byType[n.NodeType] = n
		nodeIDs = append(nodeIDs, n.ID)
	}
	// Single batch query for terminal events across every live node.
	terminals, _ := deps.Store.Events().LastTerminalByNodes(ctx, nodeIDs, nil)
	terminalView := func(id shared.UUID) *terminalEventView {
		ev, ok := terminals[id]
		if !ok {
			return nil
		}
		return &terminalEventView{
			Kind:       ev.Kind,
			OccurredAt: ev.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	// Build edges_out per type from the template's dependency graph.
	// dependencies[a] = list of types a depends on  → edges_in.
	// edges_out is the inverse.
	edgesIn := map[string][]string{}
	edgesOut := map[string][]string{}
	if template != nil {
		for _, d := range template.Spec.Nodes {
			edgesIn[d.Type] = append(edgesIn[d.Type], d.Dependencies...)
			for _, dep := range d.Dependencies {
				edgesOut[dep] = append(edgesOut[dep], d.Type)
			}
		}
	}
	out := make([]CascadeNode, 0, len(nodes))
	if template == nil {
		// No template available; project the live rows verbatim with
		// empty edge lists.
		for _, n := range nodes {
			cn := CascadeNode{
				NodeType:          n.NodeType,
				NodeID:            n.ID,
				State:             n.State,
				CurrentErrorClass: n.CurrentErrorClass,
				RetryCounter:      n.RetryCounter,
				EdgesIn:           []string{},
				EdgesOut:          []string{},
			}
			if tev := terminalView(n.ID); tev != nil {
				cn.LastTerminalEvent = tev
			}
			out = append(out, cn)
		}
		return out
	}
	for _, d := range template.Spec.Nodes {
		row, ok := byType[d.Type]
		cn := CascadeNode{
			NodeType: d.Type,
			EdgesIn:  edgesIn[d.Type],
			EdgesOut: edgesOut[d.Type],
		}
		if cn.EdgesIn == nil {
			cn.EdgesIn = []string{}
		}
		if cn.EdgesOut == nil {
			cn.EdgesOut = []string{}
		}
		if ok {
			cn.NodeID = row.ID
			cn.State = row.State
			cn.CurrentErrorClass = row.CurrentErrorClass
			cn.RetryCounter = row.RetryCounter
			if tev := terminalView(row.ID); tev != nil {
				cn.LastTerminalEvent = tev
			}
		}
		out = append(out, cn)
	}
	return out
}
