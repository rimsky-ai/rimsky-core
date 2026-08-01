// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import "sort"

// @concept: claim-co-holdership
type HoldingSubgraph struct {
	AcquirerType string
	Alias        string
	Members      []string
}

func (h HoldingSubgraph) IsHeld() bool { return len(h.Members) > 1 }

// @concept: claim-co-holdership
func HoldingSubgraphsForTemplate(spec *TemplateSpec) []HoldingSubgraph {
	if spec == nil {
		return nil
	}

	subgraphs := make(map[string]map[string]struct{})
	for _, n := range spec.Nodes {
		for _, s := range n.ClaimProducers {
			alias := s.AliasOf()
			acquirer := n.Type
			key := acquirer + "|" + alias
			if _, ok := subgraphs[key]; !ok {
				subgraphs[key] = make(map[string]struct{})
			}
			subgraphs[key][acquirer] = struct{}{}
		}
		for alias, hb := range n.Holds {
			acquirer := hb.From
			if acquirer == "" || alias == "" {
				continue
			}
			if acquirer == n.Type {
				continue
			}
			key := acquirer + "|" + alias
			if _, ok := subgraphs[key]; !ok {
				subgraphs[key] = make(map[string]struct{})
			}
			subgraphs[key][acquirer] = struct{}{}
			subgraphs[key][n.Type] = struct{}{}
		}
	}

	out := make([]HoldingSubgraph, 0, len(subgraphs))
	for key, members := range subgraphs {
		acquirer, alias := splitSubgraphKey(key)
		ms := make([]string, 0, len(members))
		for m := range members {
			ms = append(ms, m)
		}
		sort.Strings(ms)
		out = append(out, HoldingSubgraph{
			AcquirerType: acquirer,
			Alias:        alias,
			Members:      ms,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AcquirerType != out[j].AcquirerType {
			return out[i].AcquirerType < out[j].AcquirerType
		}
		return out[i].Alias < out[j].Alias
	})
	return out
}

func splitSubgraphKey(key string) (acquirer, alias string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
