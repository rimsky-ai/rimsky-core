// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"sort"
	"strings"
)

func validateHolds(n TemplateNodeDef, base string, spec *TemplateSpec, declared map[string]int, res *ValidationResult) {
	if len(n.Holds) == 0 {
		return
	}
	validateHoldsLocalAliasCollisions(n, base, res)
	for alias, binding := range n.Holds {
		hbase := fmt.Sprintf("%s.holds[%s]", base, alias)
		from := strings.TrimSpace(binding.From)
		if from == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".from",
				Msg:  "from is required",
			})
			continue
		}
		if from == n.Type {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".from",
				Msg: fmt.Sprintf(
					"holds_from_not_dependency: %q cannot hold from itself — a co-holdership pointer is not an upstream dependency of its own node",
					from),
			})
			continue
		}
		senderIdx, ok := declared[from]
		if !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".from",
				Msg: fmt.Sprintf(
					"holds_from_undeclared: %q does not name a node declared in this template",
					from),
			})
			continue
		}
		sender := spec.Nodes[senderIdx]
		if !storeAliasDeclared(sender, alias) {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase,
				Msg: fmt.Sprintf(
					"holds_unknown_claim_alias: node %q does not declare a claim alias %q in its claims: block",
					from, alias),
			})
		}
	}
}

// @concept: claim-co-holdership
func validateHoldsLocalAliasCollisions(n TemplateNodeDef, base string, res *ValidationResult) {
	byLocalAlias := make(map[string][]string, len(n.Holds))
	for alias, binding := range n.Holds {
		localAlias := effectiveHoldsLocalAlias(alias, binding)
		byLocalAlias[localAlias] = append(byLocalAlias[localAlias], alias)
	}
	localAliases := make([]string, 0, len(byLocalAlias))
	for localAlias := range byLocalAlias {
		localAliases = append(localAliases, localAlias)
	}
	sort.Strings(localAliases)
	for _, localAlias := range localAliases {
		aliases := byLocalAlias[localAlias]
		if len(aliases) < 2 {
			continue
		}
		sort.Strings(aliases)
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.holds", base),
			Msg: fmt.Sprintf(
				"holds_local_alias_collision: aliases %s resolve to the same local alias %q (via as: or the alias itself); each holds: entry must resolve to a distinct local alias",
				strings.Join(aliases, ", "), localAlias),
		})
	}
}

// @concept: claim-co-holdership
func validateHoldsAcyclic(spec *TemplateSpec, res *ValidationResult) {
	if spec == nil {
		return
	}
	edges := make(map[string][]string, len(spec.Nodes))
	for _, n := range spec.Nodes {
		for _, binding := range n.Holds {
			from := strings.TrimSpace(binding.From)
			if from == "" || from == n.Type {
				continue
			}
			edges[n.Type] = append(edges[n.Type], from)
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(edges))
	var dfs func(node string, path []string) bool
	dfs = func(node string, path []string) bool {
		color[node] = gray
		for _, next := range edges[node] {
			if color[next] == gray {
				cycle := append(append([]string{}, path...), node, next)
				res.Errors = append(res.Errors, ValidationError{
					Path: "nodes",
					Msg: fmt.Sprintf(
						"holds_from_not_dependency: holds: cycle across nodes (co-holdership must be acyclic): %s",
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
	for name := range edges {
		if color[name] == white {
			if dfs(name, nil) {
				return
			}
		}
	}
}

func storeAliasDeclared(n TemplateNodeDef, alias string) bool {
	for _, s := range n.ClaimProducers {
		if s.AliasOf() == alias {
			return true
		}
	}
	return false
}

// @concept: claim-co-holdership
func EffectiveHoldsLocalAlias(alias string, binding HoldsBinding) string {
	if as := strings.TrimSpace(binding.As); as != "" {
		return as
	}
	return alias
}

// @concept: claim-co-holdership
func resolveHeldClaimByLocalAlias(n TemplateNodeDef, localAlias string) (alias string, binding HoldsBinding, ok bool) {
	for a, b := range n.Holds {
		if EffectiveHoldsLocalAlias(a, b) == localAlias {
			return a, b, true
		}
	}
	return "", HoldsBinding{}, false
}

func validateFanOut(n TemplateNodeDef, base string, spec *TemplateSpec, declared map[string]int, hooks RegistryHooks, res *ValidationResult) {
	fo := n.FanOut
	if fo == nil {
		return
	}
	fbase := base + ".fan_out"

	claim := strings.TrimSpace(fo.Claim)
	if claim == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".claim",
			Msg:  "claim is required",
		})
		return
	}
	var producerName string
	if storeAliasDeclared(n, claim) {
		for _, s := range n.ClaimProducers {
			if s.AliasOf() == claim {
				producerName = s.Name
				break
			}
		}
	} else if senderAlias, binding, ok := resolveHeldClaimByLocalAlias(n, claim); ok {
		if senderIdx, senderDeclared := declared[binding.From]; senderDeclared && spec != nil {
			sender := spec.Nodes[senderIdx]
			for _, s := range sender.ClaimProducers {
				if s.AliasOf() == senderAlias {
					producerName = s.Name
					break
				}
			}
		}
	} else {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".claim",
			Msg: fmt.Sprintf(
				"fan_out_unknown_claim_alias: %q is not declared in claims: or holds: on this node",
				claim),
		})
		return
	}

	if producerName != "" && hooks.ClaimProducerAdvertisesSplitScope != nil {
		if !hooks.ClaimProducerAdvertisesSplitScope(producerName) {
			res.Errors = append(res.Errors, ValidationError{
				Path: fbase + ".claim",
				Msg: fmt.Sprintf(
					"fan_out requires claim_producer %q to advertise supports_split_scope",
					producerName),
			})
		}
	}

	switch fo.ErrorPolicy.Kind {
	case "", "strict", "threshold", "best_effort", "first":
	case "carry_verbatim":
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".error_policy.kind",
			Msg: fmt.Sprintf(
				"carry_verbatim_requires_single_child: node %q declares fan_out with error_policy.kind = carry_verbatim; carry-verbatim settlement requires exactly one child and fan_out declares many (use strict, threshold, best_effort, or first)",
				n.Type),
		})
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".error_policy.kind",
			Msg: fmt.Sprintf(
				"error_policy.kind = %q is not valid (one of: strict, threshold, best_effort, first)",
				fo.ErrorPolicy.Kind),
		})
	}
	if fo.ErrorPolicy.Kind == "threshold" && fo.ErrorPolicy.MaxFailures <= 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".error_policy.max_failures",
			Msg:  "max_failures must be > 0 when error_policy.kind = threshold",
		})
	}
	if fo.Parallelism < 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".parallelism",
			Msg:  "parallelism must be >= 0 (0 means unlimited)",
		})
	}
	if strings.TrimSpace(fo.PartitionRequest) == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".partition_request",
			Msg:  "partition_request is required",
		})
	}
}
