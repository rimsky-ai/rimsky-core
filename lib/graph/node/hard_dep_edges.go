// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: attribute
// @concept: cascade
// @concept: node-subscription
package node

import (
	"fmt"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type HardDepEdgeMap map[string][]string

func BuildHardDepEdges(tmpl spec.TemplateSpec) (HardDepEdgeMap, error) {
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

// @concept: cascade
// @concept: node-subscription
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
		if s.Instance {
			continue
		}
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

func detectHardDepCycle(edges HardDepEdgeMap) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)
	var path []string
	var cycles [][]string
	seenCycles := make(map[string]struct{})

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		path = append(path, node)
		defer func() {
			path = path[:len(path)-1]
		}()
		for _, next := range edges[node] {
			switch color[next] {
			case gray:
				cyclePath := append([]string(nil), path...)
				cyclePath = append(cyclePath, next)
				key := canonicalCycleKey(cyclePath)
				if _, dup := seenCycles[key]; !dup {
					seenCycles[key] = struct{}{}
					cycles = append(cycles, cyclePath)
				}
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
