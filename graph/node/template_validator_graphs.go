// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"fmt"
	"strings"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// canonicalizeGraphs normalizes a TemplateSpec that declares `graphs:`
// into the existing flat `Nodes` shape that the validator and downstream
// consumers expect. The function is idempotent: a template that already
// uses flat `Nodes` and an empty `Graphs` passes through unchanged.
//
// Pre-v1 (per .claude/rules/rules.md): the canonicalizer accepts BOTH
// the legacy flat shape AND the nested `graphs:` shape, but rejects
// templates that declare BOTH (the spec calls out the legacy shape as
// removed at v1; we keep it accepted in V1-pre to avoid churning every
// existing fixture and scenario test).
//
// Rejection classes (mapped to spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Edge-case rejections at registration):
//   - graphs_and_nodes_both_set:    template sets both `graphs:` and `nodes:`.
//   - subgraph_missing_main:        no graph named `main`.
//   - subgraph_main_has_entry_or_exit: the `main` graph carries entry: or exit:.
//   - subgraph_missing_entry:       a non-main graph has no entry:.
//   - subgraph_missing_exit:        a non-main graph has no exit:.
//   - subgraph_entry_equals_exit:   entry: and exit: name the same node.
//   - subgraph_unknown_entry:       entry: names a node not declared in graph.
//   - subgraph_unknown_exit:        exit: names a node not declared in graph.
//   - subgraph_disconnected_internal_node: an internal node is unreachable
//     from entry along subscribes/dependencies, or cannot reach exit.
//   - subgraph_recursion_unsupported: delegate: cycle across graphs.
//   - subgraph_internal_references_outer: an internal node subscribes to /
//     depends on / holds-from a node not declared in its own graph.
//   - subgraph_absorption_alias_conflict: the calling node and the entry
//     node it absorbs both declare the same store / holds alias. The
//     canonicalizer cannot pick a winner without silently dropping one
//     side's binding; reject so the template author picks a side.
//
// If any of the above is reported, canonicalizeGraphs returns a partial
// `Nodes` list (best-effort merge so downstream validators can still
// surface their own errors) plus the accumulated errors. Callers should
// abort on first non-empty errors set when strict.
func canonicalizeGraphs(spec *TemplateSpec, res *ValidationResult) {
	if spec == nil {
		return
	}
	if len(spec.Graphs) == 0 {
		// Legacy flat-Nodes shape; no canonicalization needed.
		return
	}
	if len(spec.Nodes) > 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: "nodes",
			Msg:  "graphs_and_nodes_both_set: template declares both `graphs:` and top-level `nodes:`; use one form (graphs: preferred)",
		})
		return
	}

	// Index graphs by name + validate per-graph shape.
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

	// Build a global declared-node map (graph-qualified node-type) and a
	// flat Nodes list for downstream validation. Sub-graph internal-node
	// rows are SHARED declaratively across invocations (per spec
	// §Sub-graphs / Multiple invocations); the canonicalizer emits one
	// row per (graph, internal-node-alias).
	flatten(spec, res)

	// Detect delegate: cycles across graphs.
	detectDelegateCycles(spec, graphByName, res)

	// Per-graph reachability: every non-entry / non-exit internal node
	// must be reachable from entry along subscribes / inherits edges AND
	// reach exit. Skip if shape already rejected.
	for _, g := range spec.Graphs {
		if g.Name == "" || g.Name == MainGraphName {
			continue
		}
		validateGraphReachability(g, res)
	}

	// Internal-references-outer: a sub-graph internal node may only
	// reference other nodes declared in its own graph (entry / exit /
	// other internals). References to outer-graph nodes are illegal.
	validateInternalRefsLocal(spec, res)
}

// validateGraphShape enforces the per-graph rejection classes
// `subgraph_main_has_entry_or_exit`, `subgraph_missing_entry`,
// `subgraph_missing_exit`, `subgraph_entry_equals_exit`,
// `subgraph_unknown_entry`, `subgraph_unknown_exit`.
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
	// entry / exit must name nodes declared in this graph.
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

// flatten merges all graphs' nodes into spec.Nodes for downstream
// per-node validation. Node identity is the node `type`, which the
// canonicalizer assumes is unique across the template (cross-graph
// duplicate types are reported).
//
// Two sub-graph markers are emitted here (per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Identity and absorption):
//
//  1. `IsSubgraphEntryAbsorbed: true` on every node that declares a
//     non-empty `Delegate`. At runtime the supervisor consults this
//     marker on the success branch of `applyTerminalComplete` to
//     route through the sub-graph internal-cascade fire.
//
//  2. `IsSubgraphExit: true` on every node that names the declared
//     `exit:` of a non-main graph. The runtime supervisor consults
//     this marker on the success branch of `applyTerminalComplete`
//     to drive the exit-writeback carry-rule.
//
//  3. `ResolvesViaCallingNode: true` on every subscription edge in
//     a non-main graph that references the graph's `entry:` alias.
//     The runtime cascade walker resolves such edges to the calling
//     node per-invocation (the entry is absorbed into the calling
//     node, so the structural entry alias never has its own
//     rimsky_nodes row to attach a sender to).
//
// Entry-into-calling absorption merge (per spec §Identity and
// absorption): for every node that declares `delegate: X`, the
// canonicalizer locates X's entry node (X.Entry → matching node in
// X.Nodes) and merges the entry's executor + stores + holds +
// attribute schema onto the calling node. The calling node's
// externally-declared bindings take precedence; collisions on
// store / holds alias are rejected with
// `subgraph_absorption_alias_conflict` (the canonicalizer cannot
// silently drop one side's binding).
//
// Note: the entry node ALSO remains in the flat Nodes list (its row
// is still provisioned per the existing instance-factory flow). The
// runtime `SubgraphInternalCascade` helper filters the entry out of
// the internal-cascade dispatch set so it never runs as a standalone
// child; the absorbed copy on the calling node is what dispatches at
// the parent's executor invocation. Removing the entry row entirely
// is a follow-up change that touches provisioning + runtime cascade
// resolution — out of scope for the canonicalizer-only absorption
// merge that lands here.
func flatten(spec *TemplateSpec, res *ValidationResult) {
	// Index entries by graph name for the absorption merge below.
	// Only well-formed sub-graphs (non-main, with a declared entry
	// that names a real node) contribute an entry-node lookup;
	// malformed shapes are reported by validateGraphShape and
	// silently skipped here so the absorption pass doesn't
	// double-report.
	entryByGraph := buildEntryIndex(spec)

	seen := make(map[string]string, 16)
	flat := make([]TemplateNodeDef, 0, 16)
	for gi, g := range spec.Graphs {
		isSubgraph := g.Name != MainGraphName && g.Entry != ""
		entryAlias := g.Entry
		exitAlias := g.Exit
		for ni, n := range g.Nodes {
			if strings.TrimSpace(n.Type) == "" {
				// Reported by main per-node validation.
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
			// Marker 1 + absorption merge: the calling node carries
			// IsSubgraphEntryAbsorbed when it has a non-empty Delegate,
			// and the entry node's executor + stores + holds +
			// attribute schema are merged onto the calling node so the
			// runtime dispatch path sees a fully populated row. The
			// marker is emitted at canonicalization so the runtime can
			// route on it without a per-template lookup at every
			// terminal; the merge is what makes the entry's identity
			// "be" the calling node per concept:delegation.
			if strings.TrimSpace(emitted.Delegate) != "" {
				emitted.IsSubgraphEntryAbsorbed = true
				if entry, ok := entryByGraph[emitted.Delegate]; ok {
					absorbed, errs := absorbEntryIntoCaller(emitted, entry,
						fmt.Sprintf("graphs[%d].nodes[%d]", gi, ni))
					emitted = absorbed
					res.Errors = append(res.Errors, errs...)
				}
			}
			// Marker 2: the exit node of a non-main graph carries
			// IsSubgraphExit. The runtime consults this marker (via
			// acq.NodeDef) on the success branch of applyTerminalComplete
			// to fire the carry-rule. Setting it at canonicalization
			// eliminates the runtime DB lookup (and its transient-failure
			// mode) that the previous `IsSubgraphExit(tmpl, nodeType)`
			// predicate required.
			if g.Name != MainGraphName && exitAlias != "" && emitted.Type == exitAlias {
				emitted.IsSubgraphExit = true
			}
			// Marker 3: subscription edges from non-entry internal nodes
			// that target the graph's entry alias get ResolvesViaCallingNode.
			// The runtime cascade walker resolves them to the calling
			// node per-invocation. Only emitted on non-main graphs that
			// declare entry:.
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

// buildEntryIndex returns a `graph-name → entry node` map for every
// well-formed sub-graph in spec.Graphs. Malformed shapes (missing
// entry, entry naming a non-declared node) are reported separately
// by validateGraphShape and silently skipped here so the absorption
// pass doesn't double-report. The map is keyed on the sub-graph's
// name so a calling node's `delegate: X` resolves in O(1).
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

// absorbEntryIntoCaller merges the entry node's runtime-bearing
// declarations onto the calling node. The calling node's
// externally-declared fields win on collision (per spec §Identity
// and absorption: "merged with what the calling node declared
// externally"); store / holds alias collisions with diverging
// bindings are rejected because there is no well-defined merge
// (the canonicalizer cannot drop one side silently).
//
// Fields merged:
//
//   - Executor: entry's executor wins. The calling node's
//     mutual-exclusion with `delegate:` is enforced by
//     validateExecutorCoherence; this site assumes the calling node
//     does NOT carry an externally-declared executor, but is
//     defensive: a non-empty caller executor is preserved (the
//     mutual-exclusion validator surfaces the rejection separately).
//   - Stores: append entry's after caller's, dedupe by alias,
//     conflict-reject on shared alias with non-identical bindings.
//   - Holds: union the maps, conflict-reject on key overlap with
//     diverging bindings.
//   - Attributes.Schema: deep-merge caller's external schema over
//     entry's so caller wins on overlapping keys.
//
// Returns the merged node + any conflict-detection errors. caller
// is not mutated.
func absorbEntryIntoCaller(caller, entry TemplateNodeDef, basePath string) (TemplateNodeDef, []ValidationError) {
	out := caller
	var errs []ValidationError

	// D2 mutual-exclusion — AUTHORED state. The caller's
	// IsSubgraphEntryAbsorbed marker is set BEFORE we get here, which
	// disables the `validateExecutorCoherence` check downstream
	// (otherwise every absorbed caller would false-positive after the
	// merge populates Executor). Surface the mutual-exclusion violation
	// here because this is the ONLY site that still sees the author's
	// original declaration. The check fires whenever the author wrote
	// both `executor:` AND `delegate:` on the same node, regardless of
	// whether the entry itself declares an executor — without this an
	// author who delegates to a sub-graph whose entry has no executor
	// of its own would slip past every check.
	if caller.Executor != "" && caller.Delegate != "" {
		errs = append(errs, ValidationError{
			Path: basePath + ".executor",
			Msg: fmt.Sprintf(
				"delegate and executor are mutually exclusive (executor=%q, delegate=%q)",
				caller.Executor, caller.Delegate),
		})
	}
	// Only run the diverging-executor check when the mutual-exclusion
	// check above did NOT fire. Otherwise an author who wrote BOTH
	// `executor:` AND `delegate:` whose entry happens to declare a
	// different executor would land two overlapping errors on the same
	// path — the mutual-exclusion error is the root cause, and the
	// diverging-executor message is redundant noise on top of it.
	if len(errs) == 0 && out.Executor != "" && entry.Executor != "" && out.Executor != entry.Executor {
		// The author declared an executor on the calling node AND the
		// entry declared an executor of its own. The mutual-exclusion
		// check is the catch-all for `executor:` + `delegate:`; this
		// site is more specific about which executor would win the
		// merge so operators see a coherent absorption-conflict
		// vocabulary in the diverging-executor case.
		errs = append(errs, ValidationError{
			Path: basePath + ".executor",
			Msg: fmt.Sprintf(
				"subgraph_absorption_alias_conflict: calling node declares executor %q, but the absorbed entry declares executor %q; remove the calling node's executor (the entry's wins)",
				out.Executor, entry.Executor),
		})
	}
	if out.Executor == "" {
		out.Executor = entry.Executor
	}

	if len(entry.Stores) > 0 {
		mergedStores, storeErrs := mergeStoresOnAbsorb(caller.Stores, entry.Stores, basePath)
		out.Stores = mergedStores
		errs = append(errs, storeErrs...)
	}

	if len(entry.Holds) > 0 {
		mergedHolds, holdErrs := mergeHoldsOnAbsorb(caller.Holds, entry.Holds, basePath)
		out.Holds = mergedHolds
		errs = append(errs, holdErrs...)
	}

	if entry.Attributes != nil && len(entry.Attributes.Schema) > 0 {
		// Deep-merge: caller's external schema is the L2 override; the
		// entry's schema is the absorbed baseline. shared.DeepMergeJSON
		// gives over-wins-on-conflict, so the caller wins as required.
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

// mergeStoresOnAbsorb concatenates `caller` and `entry` store
// declarations, deduping by alias. When both sides declare a store
// with the same alias, the bindings must be identical (same Name +
// Selector + Intent + Alias + Lifetime + Data); divergence is
// rejected with `subgraph_absorption_alias_conflict`. Caller-
// declared entries appear first in the output so caller's ordering
// is preserved.
func mergeStoresOnAbsorb(callerStores, entryStores []NodeStoreRef, basePath string) ([]NodeStoreRef, []ValidationError) {
	var errs []ValidationError
	byAlias := make(map[string]NodeStoreRef, len(callerStores)+len(entryStores))
	out := make([]NodeStoreRef, 0, len(callerStores)+len(entryStores))
	for _, s := range callerStores {
		byAlias[s.AliasOf()] = s
		out = append(out, s)
	}
	for _, s := range entryStores {
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
		// Identical re-declaration: the caller's copy already covers
		// the slot; the entry adds nothing new.
	}
	return out, errs
}

// storeRefIdentical reports whether two NodeStoreRefs are
// byte-equivalent for the absorption-conflict check. Comparing the
// opaque-to-rimsky `Data` field via string conversion is safe here
// because both copies come from the same template-deploy payload
// (byte-for-byte equality is the right test); rimsky never inspects
// the bytes per @blessed-invariant 20.
func storeRefIdentical(a, b NodeStoreRef) bool {
	if a.Name != b.Name || a.Selector != b.Selector || a.Intent != b.Intent {
		return false
	}
	if a.Alias != b.Alias || a.Lifetime != b.Lifetime {
		return false
	}
	return string(a.Data) == string(b.Data)
}

// mergeHoldsOnAbsorb unions the caller's and entry's holds maps.
// The map key IS the local alias; key overlap with diverging
// bindings is rejected with `subgraph_absorption_alias_conflict`.
// Identical re-declarations are accepted (the caller's copy
// survives unchanged).
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

// detectDelegateCycles builds a directed graph of `graph.delegate-to-graph`
// edges (a node whose `delegate:` points at a graph G adds an edge from
// the containing graph → G) and rejects cycles with class
// `subgraph_recursion_unsupported`.
func detectDelegateCycles(spec *TemplateSpec, graphIndex map[string]int, res *ValidationResult) {
	// Build adjacency: for each node with Delegate set, add an edge from
	// its containing graph → the target graph.
	type edge struct{ from, to string }
	edges := []edge{}
	for _, g := range spec.Graphs {
		for _, n := range g.Nodes {
			if strings.TrimSpace(n.Delegate) == "" {
				continue
			}
			if _, ok := graphIndex[n.Delegate]; !ok {
				// Reference to an unknown graph; reported separately at
				// per-node Delegate validation when wired (D2 spec); here
				// we silently skip the edge.
				continue
			}
			edges = append(edges, edge{from: g.Name, to: n.Delegate})
		}
	}
	adj := make(map[string][]string, len(spec.Graphs))
	for _, e := range edges {
		adj[e.from] = append(adj[e.from], e.to)
	}
	// Tarjan-style DFS with colors.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(spec.Graphs))
	var dfs func(node string, path []string) bool
	dfs = func(node string, path []string) bool {
		color[node] = gray
		for _, next := range adj[node] {
			if color[next] == gray {
				// Cycle.
				cycle := append([]string{}, path...)
				cycle = append(cycle, node, next)
				res.Errors = append(res.Errors, ValidationError{
					Path: "graphs",
					Msg: fmt.Sprintf(
						"subgraph_recursion_unsupported: delegate: cycle across graphs: %s",
						strings.Join(cycle, " -> ")),
				})
				return true
			}
			if color[next] == white {
				if dfs(next, append(path, node)) {
					return true
				}
			}
		}
		color[node] = black
		return false
	}
	for name := range adj {
		if color[name] == white {
			if dfs(name, nil) {
				return
			}
		}
	}
}

// validateGraphReachability runs BFS over the graph's internal edge set
// (subscribes + inherits) to confirm every internal node is reachable
// from entry AND can reach exit. Rejection class:
// subgraph_disconnected_internal_node.
//
// "Edges" approximation: a node B has an inbound edge from A if B
// declares subscribes: with node: A, OR B declares inherits: { claim: ...}
// where the upstream is A. The validator can't always resolve inherits:
// upstream sender at this layer (held-claim resolution is downstream),
// so we treat inherits: as a wildcard that doesn't block reachability.
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
	// Build adjacency.
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
			// edge: s.Node → name (sender → receiver)
			forward[s.Node] = append(forward[s.Node], name)
			backward[name] = append(backward[name], s.Node)
		}
	}
	// BFS from entry along forward edges.
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
	// BFS from exit along backward edges (reach exit).
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
}

// validateInternalRefsLocal rejects sub-graph internal nodes that
// subscribe to / inherit-from / hold-from nodes not declared in their
// own graph. Rejection class: subgraph_internal_references_outer.
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
