// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func canonicalizeGraphs(spec *TemplateSpec, res *ValidationResult) {
	if spec == nil {
		return
	}
	if len(spec.Graphs) == 0 {
		return
	}
	if len(spec.Nodes) > 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: "nodes",
			Msg:  "graphs_and_nodes_both_set: template declares both `graphs:` and top-level `nodes:`; use one form (graphs: preferred)",
		})
		return
	}

	graphByName := make(map[string]int, len(spec.Graphs))
	mainCount := 0
	for i, g := range spec.Graphs {
		base := fmt.Sprintf("graphs[%d]", i)
		name := strings.TrimSpace(g.Name)
		if name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".name", Msg: "graph name is required",
			})
			continue
		}
		if prev, dup := graphByName[name]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".name",
				Msg:  fmt.Sprintf("duplicate graph name %q (already at graphs[%d])", name, prev),
			})
			continue
		}
		graphByName[name] = i
		if name == MainGraphName {
			mainCount++
		}
		validateGraphShape(g, base, res)
	}
	if mainCount == 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: "graphs",
			Msg:  "subgraph_missing_main: no graph named \"main\"; exactly one graph must be named \"main\"",
		})
	}

	flatten(spec, res)

	detectDelegateCycles(spec, graphByName, res)

	for _, g := range spec.Graphs {
		if g.Name == "" || g.Name == MainGraphName {
			continue
		}
		validateGraphReachability(g, res)
	}

	validateInternalRefsLocal(spec, res)
}

func validateGraphShape(g GraphSpec, base string, res *ValidationResult) {
	hasEntry := strings.TrimSpace(g.Entry) != ""
	hasExit := strings.TrimSpace(g.Exit) != ""
	if g.Name == MainGraphName {
		if hasEntry || hasExit {
			res.Errors = append(res.Errors, ValidationError{
				Path: base,
				Msg: fmt.Sprintf(
					"subgraph_main_has_entry_or_exit: graph %q is the main graph and must not declare entry: or exit:",
					g.Name),
			})
		}
		return
	}
	if !hasEntry {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".entry",
			Msg:  fmt.Sprintf("subgraph_missing_entry: graph %q is a sub-graph and must declare entry:", g.Name),
		})
	}
	if !hasExit {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".exit",
			Msg:  fmt.Sprintf("subgraph_missing_exit: graph %q is a sub-graph and must declare exit:", g.Name),
		})
	}
	if hasEntry && hasExit && g.Entry == g.Exit {
		res.Errors = append(res.Errors, ValidationError{
			Path: base,
			Msg: fmt.Sprintf(
				"subgraph_entry_equals_exit: graph %q declares entry == exit (%q); they must differ",
				g.Name, g.Entry),
		})
	}
	declared := make(map[string]struct{}, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.Type != "" {
			declared[n.Type] = struct{}{}
		}
	}
	if hasEntry {
		if _, ok := declared[g.Entry]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".entry",
				Msg: fmt.Sprintf(
					"subgraph_unknown_entry: graph %q entry: %q does not name a node declared in this graph",
					g.Name, g.Entry),
			})
		}
	}
	if hasExit {
		if _, ok := declared[g.Exit]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".exit",
				Msg: fmt.Sprintf(
					"subgraph_unknown_exit: graph %q exit: %q does not name a node declared in this graph",
					g.Name, g.Exit),
			})
		}
	}
}

func flatten(spec *TemplateSpec, res *ValidationResult) {
	entryByGraph := buildEntryIndex(spec)

	seen := make(map[string]string, 16)
	flat := make([]TemplateNodeDef, 0, 16)
	for gi, g := range spec.Graphs {
		isSubgraph := g.Name != MainGraphName && g.Entry != ""
		entryAlias := g.Entry
		exitAlias := g.Exit
		for ni, n := range g.Nodes {
			if strings.TrimSpace(n.Type) == "" {
				flat = append(flat, n)
				continue
			}
			if existing, dup := seen[n.Type]; dup {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("graphs[%d].nodes[%d].type", gi, ni),
					Msg: fmt.Sprintf(
						"duplicate node type %q across graphs (also declared in %s)",
						n.Type, existing),
				})
				continue
			}
			seen[n.Type] = fmt.Sprintf("graph %q", g.Name)
			emitted := n
			if strings.TrimSpace(emitted.Delegate) != "" {
				if entry, ok := entryByGraph[emitted.Delegate]; ok {
					emitted.IsSubgraphEntryAbsorbed = true
					absorbed, errs := absorbEntryIntoCaller(emitted, entry,
						fmt.Sprintf("graphs[%d].nodes[%d]", gi, ni))
					emitted = absorbed
					res.Errors = append(res.Errors, errs...)
				}
			}
			if g.Name != MainGraphName && exitAlias != "" && emitted.Type == exitAlias {
				emitted.IsSubgraphExit = true
			}
			if isSubgraph && emitted.Type != entryAlias {
				for si := range emitted.Subscribes {
					if emitted.Subscribes[si].Node == entryAlias {
						emitted.Subscribes[si].ResolvesViaCallingNode = true
					}
				}
			}
			flat = append(flat, emitted)
		}
	}
	spec.Nodes = flat
}

func buildEntryIndex(spec *TemplateSpec) map[string]TemplateNodeDef {
	out := make(map[string]TemplateNodeDef, len(spec.Graphs))
	for _, g := range spec.Graphs {
		if g.Name == MainGraphName || g.Entry == "" {
			continue
		}
		for _, n := range g.Nodes {
			if n.Type == g.Entry {
				out[g.Name] = n
				break
			}
		}
	}
	return out
}

func absorbEntryIntoCaller(caller, entry TemplateNodeDef, basePath string) (TemplateNodeDef, []ValidationError) {
	out := caller
	var errs []ValidationError

	if caller.Executor != "" && caller.Delegate != "" {
		errs = append(errs, ValidationError{
			Path: basePath + ".executor",
			Msg: fmt.Sprintf(
				"delegate and executor are mutually exclusive (executor=%q, delegate=%q); "+
					"a delegating node inherits its executor from the sub-graph's absorbed entry and must not declare its own",
				caller.Executor, caller.Delegate),
		})
	}
	if out.Executor == "" {
		out.Executor = entry.Executor
	}

	if len(entry.ClaimProducers) > 0 {
		mergedClaimProducers, storeErrs := mergeClaimProducersOnAbsorb(caller.ClaimProducers, entry.ClaimProducers, basePath)
		out.ClaimProducers = mergedClaimProducers
		errs = append(errs, storeErrs...)
	}

	if len(entry.Holds) > 0 {
		mergedHolds, holdErrs := mergeHoldsOnAbsorb(caller.Holds, entry.Holds, basePath)
		out.Holds = mergedHolds
		errs = append(errs, holdErrs...)
	}

	if entry.Attributes != nil && len(entry.Attributes.Schema) > 0 {
		entryAsAny := any(entry.Attributes.Schema)
		var callerAsAny any
		if caller.Attributes != nil && len(caller.Attributes.Schema) > 0 {
			callerAsAny = any(caller.Attributes.Schema)
		}
		mergedAny := shared.DeepMergeJSON(entryAsAny, callerAsAny)
		if m, ok := mergedAny.(map[string]any); ok && len(m) > 0 {
			out.Attributes = &NodeAttributesDef{Schema: m}
		}
	}

	return out, errs
}

func mergeClaimProducersOnAbsorb(callerClaimProducers, entryClaimProducers []NodeClaimProducerRef, basePath string) ([]NodeClaimProducerRef, []ValidationError) {
	var errs []ValidationError
	byAlias := make(map[string]NodeClaimProducerRef, len(callerClaimProducers)+len(entryClaimProducers))
	out := make([]NodeClaimProducerRef, 0, len(callerClaimProducers)+len(entryClaimProducers))
	for _, s := range callerClaimProducers {
		byAlias[s.AliasOf()] = s
		out = append(out, s)
	}
	for _, s := range entryClaimProducers {
		alias := s.AliasOf()
		existing, dup := byAlias[alias]
		if !dup {
			byAlias[alias] = s
			out = append(out, s)
			continue
		}
		if !storeRefIdentical(existing, s) {
			errs = append(errs, ValidationError{
				Path: fmt.Sprintf("%s.stores[%s]", basePath, alias),
				Msg: fmt.Sprintf(
					"subgraph_absorption_alias_conflict: store alias %q declared on both the calling node and the absorbed entry with diverging bindings; rename one side",
					alias),
			})
			continue
		}
	}
	return out, errs
}

func storeRefIdentical(a, b NodeClaimProducerRef) bool {
	if a.Name != b.Name || a.Selector != b.Selector || a.Intent != b.Intent {
		return false
	}
	if a.Alias != b.Alias || a.Lifetime != b.Lifetime {
		return false
	}
	return string(a.Data) == string(b.Data)
}

func mergeHoldsOnAbsorb(callerHolds, entryHolds map[string]HoldsBinding, basePath string) (map[string]HoldsBinding, []ValidationError) {
	var errs []ValidationError
	out := make(map[string]HoldsBinding, len(callerHolds)+len(entryHolds))
	for k, v := range callerHolds {
		out[k] = v
	}
	for k, v := range entryHolds {
		if existing, dup := out[k]; dup {
			if existing != v {
				errs = append(errs, ValidationError{
					Path: fmt.Sprintf("%s.holds[%s]", basePath, k),
					Msg: fmt.Sprintf(
						"subgraph_absorption_alias_conflict: holds alias %q declared on both the calling node and the absorbed entry with diverging bindings; rename one side",
						k),
				})
			}
			continue
		}
		out[k] = v
	}
	return out, errs
}

// @concept: delegation
func validateDelegateTargets(spec *TemplateSpec, res *ValidationResult) {
	subGraphNames := make(map[string]struct{}, len(spec.Graphs))
	for _, g := range spec.Graphs {
		if g.Name == "" || g.Name == MainGraphName {
			continue
		}
		if strings.TrimSpace(g.Entry) == "" || strings.TrimSpace(g.Exit) == "" {
			continue
		}
		subGraphNames[g.Name] = struct{}{}
	}
	for i, n := range spec.Nodes {
		delegate := strings.TrimSpace(n.Delegate)
		if delegate == "" {
			continue
		}
		if _, ok := subGraphNames[delegate]; ok {
			continue
		}
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("nodes[%d].delegate", i),
			Msg: fmt.Sprintf(
				"subgraph_unknown_delegate_target: delegate %q does not name a declared sub-graph (with both entry: and exit:) in this template",
				delegate),
		})
	}
}

func detectDelegateCycles(spec *TemplateSpec, graphIndex map[string]int, res *ValidationResult) {
	adj := make(map[string][]string, len(spec.Graphs))
	for _, g := range spec.Graphs {
		for _, n := range g.Nodes {
			if strings.TrimSpace(n.Delegate) == "" {
				continue
			}
			if _, ok := graphIndex[n.Delegate]; !ok {
				continue
			}
			adj[g.Name] = append(adj[g.Name], n.Delegate)
		}
	}
	cycles := findAllCycles(adj)
	if len(cycles) == 0 {
		return
	}
	res.Errors = append(res.Errors, ValidationError{
		Path: "graphs",
		Msg: fmt.Sprintf(
			"subgraph_recursion_unsupported: delegate: cycle across graphs: %s",
			strings.Join(cycles[0], " -> ")),
	})
}

func validateGraphReachability(g GraphSpec, res *ValidationResult) {
	if g.Entry == "" || g.Exit == "" {
		return
	}
	nodes := make(map[string]TemplateNodeDef, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.Type != "" {
			nodes[n.Type] = n
		}
	}
	forward := make(map[string][]string, len(nodes))
	backward := make(map[string][]string, len(nodes))
	for name, n := range nodes {
		for _, s := range n.Subscribes {
			if s.Node == "" {
				continue
			}
			if _, ok := nodes[s.Node]; !ok {
				continue
			}
			forward[s.Node] = append(forward[s.Node], name)
			backward[name] = append(backward[name], s.Node)
		}
	}
	reachable := make(map[string]bool, len(nodes))
	queue := []string{g.Entry}
	reachable[g.Entry] = true
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		for _, next := range forward[head] {
			if reachable[next] {
				continue
			}
			reachable[next] = true
			queue = append(queue, next)
		}
	}
	canReachExit := make(map[string]bool, len(nodes))
	queue = []string{g.Exit}
	canReachExit[g.Exit] = true
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		for _, next := range backward[head] {
			if canReachExit[next] {
				continue
			}
			canReachExit[next] = true
			queue = append(queue, next)
		}
	}
	for name := range nodes {
		if name == g.Entry || name == g.Exit {
			continue
		}
		if !reachable[name] || !canReachExit[name] {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("graphs[%q].nodes[%q]", g.Name, name),
				Msg: fmt.Sprintf(
					"subgraph_disconnected_internal_node: node %q in graph %q is not reachable from entry %q or cannot reach exit %q along subscribes edges",
					name, g.Name, g.Entry, g.Exit),
			})
		}
	}
	if g.Exit != g.Entry && !reachable[g.Exit] {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("graphs[%q]", g.Name),
			Msg: fmt.Sprintf(
				"subgraph_exit_unreachable: graph %q exit %q is not reachable from entry %q along subscribes edges",
				g.Name, g.Exit, g.Entry),
		})
	}
}

func validateInternalRefsLocal(spec *TemplateSpec, res *ValidationResult) {
	for _, g := range spec.Graphs {
		if g.Name == MainGraphName {
			continue
		}
		local := make(map[string]struct{}, len(g.Nodes))
		for _, n := range g.Nodes {
			if n.Type != "" {
				local[n.Type] = struct{}{}
			}
		}
		for _, n := range g.Nodes {
			gbase := fmt.Sprintf("graphs[%q].nodes[%q]", g.Name, n.Type)
			for si, s := range n.Subscribes {
				if s.Node == "" {
					continue
				}
				if _, ok := local[s.Node]; !ok {
					res.Errors = append(res.Errors, ValidationError{
						Path: fmt.Sprintf("%s.subscribes[%d].node", gbase, si),
						Msg: fmt.Sprintf(
							"subgraph_internal_references_outer: node %q in graph %q subscribes to %q which is not declared in this sub-graph",
							n.Type, g.Name, s.Node),
					})
				}
			}
			for alias, hb := range n.Holds {
				if hb.From == "" {
					continue
				}
				if _, ok := local[hb.From]; !ok {
					res.Errors = append(res.Errors, ValidationError{
						Path: fmt.Sprintf("%s.holds[%q].from", gbase, alias),
						Msg: fmt.Sprintf(
							"subgraph_internal_references_outer: node %q in graph %q holds-from %q which is not declared in this sub-graph",
							n.Type, g.Name, hb.From),
					})
				}
			}
		}
	}
}
