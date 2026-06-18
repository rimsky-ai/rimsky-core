// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"strings"
)

func validateHolds(n TemplateNodeDef, base string, spec *TemplateSpec, declared map[string]int, res *ValidationResult) {
	if len(n.Holds) == 0 {
		return
	}
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
		senderIdx, ok := declared[from]
		if !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".from",
				Msg: fmt.Sprintf(
					"holds_from_not_dependency: %q does not name a node declared in this template",
					from),
			})
			continue
		}
		sender := spec.Nodes[senderIdx]
		claimAlias := alias
		if strings.TrimSpace(binding.As) != "" {
			claimAlias = alias
		}
		if !storeAliasDeclared(sender, claimAlias) {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase,
				Msg: fmt.Sprintf(
					"holds_unknown_claim_alias: node %q does not declare a claim alias %q in its claims: block",
					from, claimAlias),
			})
		}
	}
}

func storeAliasDeclared(n TemplateNodeDef, alias string) bool {
	for _, s := range n.Stores {
		if s.AliasOf() == alias {
			return true
		}
	}
	return false
}

func validateFanOut(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	fo := n.FanOut
	if fo == nil {
		return
	}
	fbase := base + ".fan_out"

	if strings.TrimSpace(n.Delegate) != "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase,
			Msg:  "delegate and fan_out are mutually exclusive — a calling node cannot itself fan-out (the sub-graph's entry can declare fan_out instead)",
		})
		return
	}

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
		for _, s := range n.Stores {
			if s.AliasOf() == claim {
				producerName = s.Name
				break
			}
		}
	} else if _, ok := n.Holds[claim]; ok {
	} else {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".claim",
			Msg: fmt.Sprintf(
				"fan_out_unknown_claim_alias: %q is not declared in claims: or holds: on this node",
				claim),
		})
		return
	}

	if producerName != "" && hooks.StoreAdvertisesSplitScope != nil {
		if !hooks.StoreAdvertisesSplitScope(producerName) {
			res.Errors = append(res.Errors, ValidationError{
				Path: fbase + ".claim",
				Msg: fmt.Sprintf(
					"fan_out requires store %q to advertise supports_split_scope",
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
	if fo.ErrorPolicy.Kind != "strict" && fo.ErrorPolicy.CancelSiblings {
		res.Errors = append(res.Errors, ValidationError{
			Path: fbase + ".error_policy.cancel_siblings",
			Msg:  "cancel_siblings is only meaningful when error_policy.kind = strict",
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
