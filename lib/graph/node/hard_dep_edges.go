// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Upstream-refresh edge map. Computed at template registration alongside
// the subscription-edge map (subscription_edges.go); consumed by the
// cascade walker at runtime to proactively invalidate upstreams that a
// receiver names in a subscribes: entry with force_upstream_refresh: true.
//
// Note the key-direction difference from SubscriptionEdgeMap:
// subscription edges are keyed by SENDER (downstream lookup from a
// transitioning sender); these edges are keyed by RECEIVER (upstream
// lookup from a freshly-invalidated receiver).
//
//	@concept: attribute
//	@concept: cascade
//	@concept: node-subscription
package node

import (
	"fmt"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// HardDepEdgeMap is keyed by receiver node-type. The value is the
// list of upstream node-types the receiver requires invalidated in
// the same frame.
type HardDepEdgeMap map[string][]string

// BuildHardDepEdges walks every node's subscribes: entries looking for
// entries that carry force_upstream_refresh: true. Produces a map from
// receiver node-type to the set of sender node-types. Performs cycle
// detection on the resulting graph; returns an error describing every
// cycle found (multiple disjoint cycles are collected into a single
// error so template authors can fix all topology issues in one round).
//
// Also rejects force-refresh targets that fan out: the runtime
// pullForceRefreshUpstreams picks a single upstream node per type per
// instance, which is ambiguous for a fan-out sender (multiple
// `rimsky_nodes` rows of the same NodeType in the same instance). A
// future spec extension could iterate, but today we reject at
// registration so the wait-set semantics stay unambiguous.
func BuildHardDepEdges(tmpl spec.TemplateSpec) (HardDepEdgeMap, error) {
	// @deliberate: Index node-types that declare `fan_out:` so we can
	// reject force_upstream_refresh edges pointing at them.
	fanoutTypes := make(map[string]struct{})
	for _, n := range tmpl.Nodes {
		if n.FanOut != nil {
			fanoutTypes[n.Type] = struct{}{}
		}
	}
	out := HardDepEdgeMap{}
	var fanoutViolations []string
	for _, n := range tmpl.Nodes {
		senders := hardDepSendersOf(n)
		for _, s := range senders {
			if _, isFanout := fanoutTypes[s]; isFanout {
				fanoutViolations = append(fanoutViolations,
					fmt.Sprintf("receiver %q -> sender %q", n.Type, s))
			}
		}
		if len(senders) > 0 {
			out[n.Type] = senders
		}
	}
	if len(fanoutViolations) > 0 {
		return nil, fmt.Errorf(
			"force_upstream_refresh targets a fan-out node-type (not supported — single-instance "+
				"per template required): %s",
			strings.Join(fanoutViolations, "; "))
	}
	if err := detectHardDepCycle(out); err != nil {
		return nil, err
	}
	return out, nil
}

// hardDepSendersOf returns the upstream node-types named by subscription
// entries on n that carry force_upstream_refresh: true. Excludes self-
// references (a node doesn't pull itself) and cross-cutting
// (Instance=true) entries (sender-agnostic; rejected at registration).
//
//	@concept: cascade
//	@concept: node-subscription
func hardDepSendersOf(n TemplateNodeDef) []string {
	if len(n.Subscribes) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, s := range n.Subscribes {
		if s.ForceUpstreamRefresh == nil || !*s.ForceUpstreamRefresh {
			continue
		}
		// @deliberate: Cross-cutting subscriptions (Instance=true) cannot
		// carry force_upstream_refresh; the validator rejects that
		// combination. Skip defensively so this builder doesn't produce
		// sender-agnostic edges if a malformed input slips past validation.
		if s.Instance {
			continue
		}
		// @constraint: a node doesn't pull itself — skip empty/self-target entries.
		if s.Node == "" || s.Node == n.Type {
			continue
		}
		if _, dup := seen[s.Node]; dup {
			continue
		}
		seen[s.Node] = struct{}{}
		out = append(out, s.Node)
	}
	return out
}

// detectHardDepCycle does a DFS over the hard-dep edge graph and
// returns a descriptive error listing every cycle found. Multiple
// disjoint cycles are reported together so template authors can fix
// all topology issues in one round; iteration order over the input
// map is non-deterministic but the cycle list within each error is
// stable for a given traversal start.
func detectHardDepCycle(edges HardDepEdgeMap) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)
	var path []string
	// @deliberate: Collected cycles, each represented as the path of node-types
	// involved (closing with the repeated entry). Accumulated across
	// DFS roots; a non-empty slice at the end produces the aggregate
	// error.
	var cycles [][]string
	// @deliberate: Deduplicate cycles by their canonical-form key so a
	// cycle discovered from multiple DFS roots only reports once.
	seenCycles := make(map[string]struct{})

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		path = append(path, node)
		// @deliberate: deferred pop mirrors the gray→black colour
		// transition — even when a cycle is recorded, the path slice is
		// restored to its caller-visible state so sibling DFS branches
		// see a clean path.
		defer func() {
			path = path[:len(path)-1]
		}()
		for _, next := range edges[node] {
			switch color[next] {
			case gray:
				// @deliberate: cycle detected. Copy path before appending
				// to avoid sharing backing array with the live `path`
				// slice.
				cyclePath := append([]string(nil), path...)
				cyclePath = append(cyclePath, next)
				key := canonicalCycleKey(cyclePath)
				if _, dup := seenCycles[key]; !dup {
					seenCycles[key] = struct{}{}
					cycles = append(cycles, cyclePath)
				}
				// @deliberate: continue searching other edges so further
				// cycles on disjoint components get reported in the same
				// error.
			case white:
				dfs(next)
			}
		}
		color[node] = black
	}

	for receiver := range edges {
		if color[receiver] == white {
			dfs(receiver)
		}
	}
	if len(cycles) == 0 {
		return nil
	}
	if len(cycles) == 1 {
		return fmt.Errorf("hard-dep cycle detected: %v", cycles[0])
	}
	return fmt.Errorf("hard-dep cycles detected (%d): %v", len(cycles), cycles)
}

// canonicalCycleKey returns a deterministic key for a cycle path
// (e.g. [A B C A] and [B C A B] map to the same key) so the same
// cycle visited from different DFS roots is reported once. Rotates
// the cycle so the lexicographically smallest node-type starts the
// sequence; the closing repetition is stripped before rotation.
//
// The `len(path) < 2` early-return is defensive: a real hard-dep
// cycle path always has at least two entries (the starting node and
// its closing repetition). Single-node self-cycles can't reach this
// function because `hardDepSendersOf` filters self-references
// upstream (`s.Node == n.Type` is skipped). The guard stays in case
// a future refactor changes that filter or the DFS starts emitting
// one-node paths through some other route.
func canonicalCycleKey(path []string) string {
	if len(path) < 2 {
		return strings.Join(path, "→")
	}
	body := path[:len(path)-1]
	minIdx := 0
	for i := 1; i < len(body); i++ {
		if body[i] < body[minIdx] {
			minIdx = i
		}
	}
	rotated := make([]string, 0, len(body))
	rotated = append(rotated, body[minIdx:]...)
	rotated = append(rotated, body[:minIdx]...)
	return strings.Join(rotated, "→")
}
