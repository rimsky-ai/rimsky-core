package observability

import (
	"context"

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
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
// set.
func computeCascadeGraph(ctx context.Context, deps Deps, _ persistence.InstanceRow, nodes []persistence.NodeRow, template *persistence.TemplateRow) []CascadeNode {
	// Index live nodes by node_type for O(1) projection.
	byType := make(map[string]persistence.NodeRow, len(nodes))
	for _, n := range nodes {
		byType[n.NodeType] = n
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
			out = append(out, CascadeNode{
				NodeType:          n.NodeType,
				NodeID:            n.ID,
				State:             n.State,
				CurrentErrorClass: n.CurrentErrorClass,
				RetryCounter:      n.RetryCounter,
				EdgesIn:           []string{},
				EdgesOut:          []string{},
			})
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
			tev := lookupLastTerminalEvent(ctx, deps, row.ID)
			if tev != nil {
				cn.LastTerminalEvent = tev
			}
		}
		out = append(out, cn)
	}
	return out
}

// lookupLastTerminalEvent fetches the most recent rimsky_events row
// for nodeID whose kind is in the dispatch-terminal set
// (work_completed | error). Returns nil when there is no such row.
func lookupLastTerminalEvent(ctx context.Context, deps Deps, nodeID shared.UUID) *terminalEventView {
	res, err := deps.Store.Events().List(ctx, persistence.EventListFilter{NodeID: &nodeID}, persistence.ListPagination{Limit: 25}, nil)
	if err != nil {
		return nil
	}
	for _, ev := range res.Events {
		switch ev.Kind {
		case "work_completed", "error":
			return &terminalEventView{
				Kind:       ev.Kind,
				OccurredAt: ev.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
			}
		}
	}
	return nil
}
