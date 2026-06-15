// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Holding-subgraph computation (spec §18.4). Runs at template deploy.
//
// The holding subgraph for a held claim is `{acquirer} ∪ {co-holders}`.
// Co-holdership is direct only — does not propagate transitively
// through dep chains. Each downstream node that needs the live claim
// declares it explicitly via `holds: {<alias>: {from: <acquirer>}}`.
//
// Per the 2026-04-30 stores cleanup, no per-alias claim_resolutions
// validation runs at deploy: store disposition (Commit / Abandon) is
// governed by per-store config, not by template-level declarations.
//
// Pick-policy intent enforcement (per spec §4.6: pick-policy claims
// must be intent: rw) needs the operator's store registry; that check
// lives in T23 (registry-dependent).

package node

import "sort"

// HoldingSubgraph is one held claim's deploy-time metadata: the
// acquirer node type, the alias, and the set of node types in its
// holding subgraph (acquirer + every co-holder of the alias).
type HoldingSubgraph struct {
	AcquirerType string
	Alias        string
	// @constraint: members is sorted and includes the acquirer.
	Members []string
}

// IsHeld reports whether this claim is held — i.e., the subgraph has
// at least one co-holder (declared via `holds:`) beyond the acquirer.
func (h HoldingSubgraph) IsHeld() bool { return len(h.Members) > 1 }

// HoldingSubgraphsForTemplate computes the materialized holding
// subgraphs for every (acquirer, alias) pair. A claim is held when its
// subgraph has at least one co-holder beyond the acquirer. Returned for
// runtime use by the supervisor's auto-terminal mechanism.
//
// Members come from `holds:` (the sole co-holdership directive): each
// `holds:` entry `{<alias>: {from: <acquirer-type>}}` adds the
// declaring node to the (<acquirer-type>, <alias>) subgraph. The outer
// key IS the acquirer's claim alias (the template validator enforces
// that the `from:` node declares that alias on its `stores:` block),
// and `from:` names the acquirer node-type directly — no deps-walk is
// needed to route a holds edge to its acquirer.
//
// For non-held claims (subgraph size == 1, i.e. only the acquirer),
// the result includes a HoldingSubgraph with a single member; the
// supervisor checks IsHeld() to decide whether to insert
// rimsky_claim_holders rows at acquisition.
//
// Returns subgraphs sorted by (acquirer node type, alias) for
// deterministic iteration.
func HoldingSubgraphsForTemplate(spec *TemplateSpec) []HoldingSubgraph {
	if spec == nil {
		return nil
	}

	// @constraint: subgraphs is keyed `acquirer|alias` → members
	// set; each node joins every (acquirer, alias) subgraph it
	// acquires a store under.
	subgraphs := make(map[string]map[string]struct{})
	for _, n := range spec.Nodes {
		for _, s := range n.Stores {
			alias := s.AliasOf()
			acquirer := n.Type
			key := acquirer + "|" + alias
			if _, ok := subgraphs[key]; !ok {
				subgraphs[key] = make(map[string]struct{})
			}
			subgraphs[key][acquirer] = struct{}{}
		}
		// @deliberate: For each `holds:` co-holdership, the acquirer is named directly
		// named directly by `from:` and the outer key is the acquirer's
		// claim alias — add this node to the (acquirer, alias) subgraph.
		// A single co-holder makes the alias held (subgraph size > 1).
		for alias, hb := range n.Holds {
			acquirer := hb.From
			if acquirer == "" || alias == "" {
				continue
			}
			if acquirer == n.Type {
				// @deliberate: A node co-holding its own claim adds no member beyond
				// member beyond the acquirer it already seeded above; skip
				// to avoid a spurious self-edge.
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

// splitSubgraphKey reverses the "acquirer|alias" formatting used in
// the subgraph map.
func splitSubgraphKey(key string) (acquirer, alias string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
