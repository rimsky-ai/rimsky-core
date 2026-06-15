// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"strings"
)

// validateHolds enforces the `holds:` template directive per spec
// node co-holds an upstream claim:
//
//	holds:
//	  <local-alias>:
//	    from: <upstream-node-alias>
//
// Validation:
//   - `from:` MUST name another node declared in the same template.
//     Rejection class: holds_from_not_dependency (the upstream MUST
//     be an upstream dependency of this node — declared via
//     `subscribes:` with `type: terminal/*` from the same node).
//   - The upstream node MUST declare the referenced claim alias in
//     its `claims:` (NodeStoreRef) block. Rejection class:
//     holds_unknown_claim_alias.
//
// The first check (dependency relationship) is approximate at this
// layer: holds_from_not_dependency reduces to "the upstream node
// must subscribe-to-state or otherwise be reachable as a sender".
// Strict transitivity is enforced at run-time in the auto-terminal
// path (E3); template-time rejection catches the trivial mistakes.
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
		// @deliberate: holds_unknown_claim_alias — the upstream node MUST
		// declare the referenced claim alias in its claims: block.
		sender := spec.Nodes[senderIdx]
		claimAlias := alias
		if strings.TrimSpace(binding.As) != "" {
			// @deliberate: `as:` rebinds the local alias; the upstream
			// alias is still the outer key.
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

// storeAliasDeclared reports whether the node declares a store ref
// whose alias (or store name when alias is empty) equals the target.
func storeAliasDeclared(n TemplateNodeDef, alias string) bool {
	for _, s := range n.Stores {
		if s.AliasOf() == alias {
			return true
		}
	}
	return false
}

// validateFanOut enforces the `fan_out:` template directive per spec
// across sub-scopes of one of its `claims:` aliases.
//
// Validation:
//   - `claim:` references a claim alias declared on the node (in
//     `claims:` or `holds:`).
//   - The producer of the referenced claim advertises
//     `supports_split_scope: true` in its Capabilities snapshot
//     (gated by the registry hook; silent skip when not available).
//   - `error_policy.kind:` is one of the four shapes (strict,
//     threshold, best_effort, first).
//   - `error_policy.cancel_siblings:` is meaningful only for kind
//     `strict`.
//   - `error_policy.max_failures:` is meaningful only for kind
//     `threshold`.
func validateFanOut(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	fo := n.FanOut
	if fo == nil {
		return
	}
	fbase := base + ".fan_out"

	// @deliberate: `delegate:` + `fan_out:` is not supported. The
	// canonicalizer absorbs the sub-graph entry's executor (and stores /
	// holds / attributes) onto the calling node, but it does NOT scope
	// fan-out down into the absorbed sub-graph: every fan-out child would
	// re-fire the internal cascade as a separate parent at dispatch, each
	// thinking it's the canonical absorbed-entry caller. Reject at
	// registration so the combination can never reach the runtime. If a
	// sub-graph needs to fan out, declare `fan_out:` on the entry node
	// inside the sub-graph instead.
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
	// @deliberate: claim alias must be declared either in claims: or holds:.
	var producerName string
	if storeAliasDeclared(n, claim) {
		for _, s := range n.Stores {
			if s.AliasOf() == claim {
				producerName = s.Name
				break
			}
		}
	} else if _, ok := n.Holds[claim]; ok {
		// @deliberate: holds: claim — the producer is the upstream's.
		// The supports_split_scope check is best done against the
		// upstream's store; since this validator doesn't have the
		// instance graph yet (held claims resolve at runtime), skip the
		// capability gate here. Runtime acquisition fails if the upstream
		// producer doesn't actually support SplitScope.
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
		// @deliberate: Carry-verbatim is the delegation settlement shape and requires
		// and requires exactly one child by construction. `fan_out:`
		// declares N children (the partition count is producer-determined
		// at runtime), so the N=1 requirement is unsatisfiable here —
		// reject at canonicalization. Delegation (`delegate:`) is the
		// only shape that carries carry-verbatim, and it always declares
		// exactly one child execution; `delegate:` + `fan_out:` is
		// rejected above by mutual exclusion.
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
