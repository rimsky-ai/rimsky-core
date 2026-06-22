// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type CascadeNode struct {
	NodeType          string                      `json:"node_type"`
	NodeID            shared.UUID                 `json:"node_id"`
	RunSummary        *persistence.NodeRunSummary `json:"run_summary,omitempty"`
	LastTerminalEvent *terminalEventView          `json:"last_terminal_event,omitempty"`
	EdgesIn           []string                    `json:"edges_in"`
	EdgesOut          []string                    `json:"edges_out"`
}

type terminalEventView struct {
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurred_at"`
}

func computeCascadeGraph(ctx context.Context, deps Deps, _ persistence.InstanceRow, nodes []persistence.NodeRow, template *persistence.TemplateRow) []CascadeNode {
	byType := make(map[string]persistence.NodeRow, len(nodes))
	nodeIDs := make([]shared.UUID, 0, len(nodes))
	for _, n := range nodes {
		byType[n.NodeType] = n
		nodeIDs = append(nodeIDs, n.ID)
	}
	var (
		terminals map[shared.UUID]persistence.EventRow
		summaries = map[shared.UUID]persistence.NodeRunSummary{}
	)
	_ = deps.Tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := deps.Tables.Events().LastTerminalByNodes(ctx, nodeIDs, tx)
		if err != nil {
			return err
		}
		terminals = t
		for _, id := range nodeIDs {
			s, err := deps.Tables.Nodes().GetRunSummary(ctx, id, tx)
			if err != nil {
				return err
			}
			summaries[id] = s
		}
		return nil
	})
	summaryFor := func(id shared.UUID) *persistence.NodeRunSummary {
		s, ok := summaries[id]
		if !ok {
			return nil
		}
		sc := s
		return &sc
	}
	terminalView := func(id shared.UUID) *terminalEventView {
		ev, ok := terminals[id]
		if !ok {
			return nil
		}
		return &terminalEventView{
			Kind:       ev.KindRaw,
			OccurredAt: ev.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	edgesIn := map[string][]string{}
	edgesOut := map[string][]string{}
	if template != nil {
		for _, d := range template.Spec.Nodes {
			for _, s := range d.Subscribes {
				if s.Node == "" {
					continue
				}
				edgesIn[d.Type] = append(edgesIn[d.Type], s.Node)
				edgesOut[s.Node] = append(edgesOut[s.Node], d.Type)
			}
		}
	}
	out := make([]CascadeNode, 0, len(nodes))
	if template == nil {
		for _, n := range nodes {
			cn := CascadeNode{
				NodeType:   n.NodeType,
				NodeID:     n.ID,
				RunSummary: summaryFor(n.ID),
				EdgesIn:    []string{},
				EdgesOut:   []string{},
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
			cn.RunSummary = summaryFor(row.ID)
			if tev := terminalView(row.ID); tev != nil {
				cn.LastTerminalEvent = tev
			}
		}
		out = append(out, cn)
	}
	return out
}
