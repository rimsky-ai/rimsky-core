// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Holding-subgraph computation and inheritance validation
// (spec §18.4 / §18.5). Runs at template deploy.
//
// The holding subgraph for a held claim is `{acquirer} ∪ {direct
// inheritors}`. Inheritance is direct only — does not propagate
// transitively through dep chains. Each downstream node that needs
// the live claim declares it explicitly via `inherits: [{claim:
// <alias>}]`.
//
// At deploy time we verify that every `inherits:` reference resolves
// to a real upstream claim alias acquired by some node N₁ that this
// node depends on (transitively through deps). Per the 2026-04-30
// stores cleanup, no per-alias claim_resolutions validation runs at
// deploy: store disposition (Commit / Abandon) is governed by
// per-store config, not by template-level declarations.
//
// Pick-policy intent enforcement (per spec §4.6: pick-policy claims
// must be intent: rw) needs the operator's store registry; that check
// lives in T23 (registry-dependent).

package node

import (
	"fmt"
	"sort"
)

// HoldingSubgraph is one held claim's deploy-time metadata: the
// acquirer node type, the alias, and the set of node types in its
// holding subgraph (acquirer + direct inheritors).
type HoldingSubgraph struct {
	AcquirerType string
	Alias        string
	Members      []string // sorted; includes acquirer
}

// IsHeld reports whether this claim is held — i.e., the subgraph has
// at least one inheritor beyond the acquirer.
func (h HoldingSubgraph) IsHeld() bool { return len(h.Members) > 1 }

// ValidateInheritance walks all inherits: declarations and validates
// against the §18.4 / §18.5 rules. Appends errors to res. Run from
// inside ValidateTemplate.
//
// Duplicate alias-acquirers (two distinct nodes both naming the same
// alias on their `stores:` block) are accepted only when no
// `inherits:` references that alias. When an inheritor references a
// multiply-acquired alias, the deps-walk picks exactly one acquirer
// per inheritor; if more than one acquirer is reachable for some
// inheritor the configuration is ambiguous and rejected here.
// Otherwise the runtime `HoldingSubgraphsForTemplate` reproduces the
// same deps-walk so the deploy-time and runtime subgraph computations
// agree.
func ValidateInheritance(spec *TemplateSpec, res *ValidationResult) {
	if spec == nil {
		return
	}
	// Build acquirer index: alias → set of acquirer node types.
	acquirerByAlias := make(map[string][]string)
	seenAcq := make(map[string]map[string]struct{})
	for _, n := range spec.Nodes {
		for _, s := range n.Stores {
			alias := s.AliasOf()
			if _, ok := seenAcq[alias]; !ok {
				seenAcq[alias] = make(map[string]struct{})
			}
			if _, dup := seenAcq[alias][n.Type]; dup {
				continue
			}
			seenAcq[alias][n.Type] = struct{}{}
			acquirerByAlias[alias] = append(acquirerByAlias[alias], n.Type)
		}
	}

	// Build the dep-reachability map: nodeType → set of all its
	// transitive ancestors (the nodes it depends on, directly or via
	// chains).
	ancestors := transitiveAncestors(spec.Nodes)

	// Walk inherits: declarations.
	for i, n := range spec.Nodes {
		for j, ie := range n.Inherits {
			alias := ie.Claim
			if alias == "" {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("nodes[%d].inherits[%d].claim", i, j),
					Msg:  "inherits claim alias is required",
				})
				continue
			}
			candidates, ok := acquirerByAlias[alias]
			if !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("nodes[%d].inherits[%d].claim", i, j),
					Msg:  fmt.Sprintf("inherits references alias %q which is not acquired by any node", alias),
				})
				continue
			}
			// Find every candidate acquirer this node transitively
			// depends on. Exactly one must be reachable; zero is
			// "no upstream", more than one is ambiguous and rejected
			// here (the runtime cannot pick deterministically).
			reachable := make([]string, 0, len(candidates))
			for _, c := range candidates {
				if c == n.Type {
					continue
				}
				if _, depended := ancestors[n.Type][c]; depended {
					reachable = append(reachable, c)
				}
			}
			if len(reachable) == 0 {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("nodes[%d].inherits[%d].claim", i, j),
					Msg:  fmt.Sprintf("inherits references alias %q but no acquirer is reachable via deps from %q", alias, n.Type),
				})
				continue
			}
			if len(reachable) > 1 {
				sort.Strings(reachable)
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("nodes[%d].inherits[%d].claim", i, j),
					Msg:  fmt.Sprintf("inherits references alias %q but %d acquirers are reachable via deps from %q (%v); aliases inherited via inherits: must be unambiguous — disambiguate by giving each acquirer a distinct alias", alias, len(reachable), n.Type, reachable),
				})
				continue
			}
		}
	}
}

// HoldingSubgraphsForTemplate computes the materialized holding
// subgraphs for every (acquirer, alias) pair that has at least one
// inheritance edge. Returned for runtime use by the supervisor's
// auto-terminal mechanism.
//
// For non-held claims (subgraph size == 1, i.e. only the acquirer),
// the result includes a HoldingSubgraph with a single member; the
// supervisor checks IsHeld() to decide whether to insert
// rimsky_claim_holders rows at acquisition.
//
// Multiple-acquirer aliases (the same alias-string acquired by two
// distinct nodes) are handled via the same deps-reachability walk
// `ValidateInheritance` performs at deploy: each `inherits:` edge is
// routed to the acquirer reachable from the inheritor's node-type via
// `dependencies`. ValidateInheritance rejects the ambiguous case
// (multiple reachable acquirers per inheritor) at deploy time, so this
// function picks the first reachable acquirer per inheritor and
// produces the same subgraphs the validator emitted.
//
// Returns subgraphs sorted by (acquirer node type, alias) for
// deterministic iteration.
func HoldingSubgraphsForTemplate(spec *TemplateSpec) []HoldingSubgraph {
	if spec == nil {
		return nil
	}
	ancestors := transitiveAncestors(spec.Nodes)

	// alias → list of acquirer node types (deduped, deterministic
	// order — input order, then sorted by type for stability).
	acqsByAlias := make(map[string][]string)
	seenAcq := make(map[string]map[string]struct{})
	for _, n := range spec.Nodes {
		for _, s := range n.Stores {
			alias := s.AliasOf()
			if _, ok := seenAcq[alias]; !ok {
				seenAcq[alias] = make(map[string]struct{})
			}
			if _, dup := seenAcq[alias][n.Type]; dup {
				continue
			}
			seenAcq[alias][n.Type] = struct{}{}
			acqsByAlias[alias] = append(acqsByAlias[alias], n.Type)
		}
	}

	subgraphs := make(map[string]map[string]struct{}) // key: acquirer|alias → members set
	for _, n := range spec.Nodes {
		// Each node is a member of every (n, alias) subgraph it acquires.
		for _, s := range n.Stores {
			alias := s.AliasOf()
			acquirer := n.Type
			key := acquirer + "|" + alias
			if _, ok := subgraphs[key]; !ok {
				subgraphs[key] = make(map[string]struct{})
			}
			subgraphs[key][acquirer] = struct{}{}
		}
		// For each inherits edge, pick the unique acquirer this node
		// transitively depends on.
		for _, ie := range n.Inherits {
			alias := ie.Claim
			if alias == "" {
				continue
			}
			cands, ok := acqsByAlias[alias]
			if !ok {
				continue
			}
			var picked string
			for _, c := range cands {
				if c == n.Type {
					continue
				}
				if _, depended := ancestors[n.Type][c]; depended {
					if picked != "" {
						// Ambiguity already rejected by
						// ValidateInheritance — preserve
						// determinism by sticking with the first
						// reachable acquirer.
						break
					}
					picked = c
				}
			}
			if picked == "" {
				continue
			}
			key := picked + "|" + alias
			if _, ok := subgraphs[key]; !ok {
				subgraphs[key] = make(map[string]struct{})
			}
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

// transitiveAncestors returns, for each node type, the set of all
// node types it is reactively coupled to transitively (the closure of
// the subscription relation upward). Self is not included.
//
// Post-2026-05-14 the relation is driven by SubscriptionEntry +
// substitution-ref inference rather than the retired Dependencies
// field. We collect the direct upstream node-types from each node's
// explicit `subscribes:` entries plus any `{{nodes.<X>...}}`
// substitution refs in the node's attribute-schema sources.
//
//	@concept: node-subscription
func transitiveAncestors(nodes []TemplateNodeDef) map[string]map[string]struct{} {
	directDeps := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		directDeps[n.Type] = upstreamNodeTypes(n)
	}
	out := make(map[string]map[string]struct{}, len(nodes))
	for _, n := range nodes {
		seen := make(map[string]struct{})
		var walk func(t string)
		walk = func(t string) {
			for _, d := range directDeps[t] {
				if _, already := seen[d]; already {
					continue
				}
				seen[d] = struct{}{}
				walk(d)
			}
		}
		walk(n.Type)
		out[n.Type] = seen
	}
	return out
}

// upstreamNodeTypes returns the direct upstream node-types this node is
// reactively coupled to: explicit per-node Subscribes entries plus
// substitution refs in attribute-schema sources. Cross-cutting
// (instance:true) entries do not contribute a direct edge (the
// inheritance walker needs concrete node types).
func upstreamNodeTypes(n TemplateNodeDef) []string {
	seen := make(map[string]struct{})
	for _, s := range n.Subscribes {
		if s.Node == "" || s.Node == n.Type {
			continue
		}
		seen[s.Node] = struct{}{}
	}
	for _, ref := range parseSubstitutionRefsFromAttributes(n) {
		if ref.SenderNodeType == "" || ref.SenderNodeType == n.Type {
			continue
		}
		seen[ref.SenderNodeType] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
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
