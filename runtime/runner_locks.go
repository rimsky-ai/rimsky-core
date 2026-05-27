// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lock-spec construction + template lookup helpers.
//
// Two primitives, two types: locks.NamedLockSpec and claimproducer.ClaimSpec.
// There is no LockSpec interface; this file's helpers operate on `any`
// values and dispatch by type-switch.
//
// Deterministic ordering (blessed-invariant 3): (kind, sort_key) with
// "named" < "scope" and:
//   - NamedLockSpec sort key: Name
//   - ClaimSpec sort key:     ProducerName + ":" + Selector (post-substitution)

package runtime

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/rimsky-ai/rimsky-core/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
)

// sortLockSpecs orders specs by (kind, sort_key) per blessed-invariant
// 3. Inputs may be NamedLockSpec or ClaimSpec values; unknown types
// sort last.
func sortLockSpecs(specs []any) {
	sort.SliceStable(specs, func(i, j int) bool {
		ki, kj := kindForSpec(specs[i]), kindForSpec(specs[j])
		if ki != kj {
			return ki < kj
		}
		return sortKeyForSpec(specs[i]) < sortKeyForSpec(specs[j])
	})
}

// kindForSpec returns the kind tag for a spec value.
// "named" < "scope"; the lexical ordering matches the spec table
// (blessed-invariant 3).
func kindForSpec(sp any) string {
	switch sp.(type) {
	case locks.NamedLockSpec:
		return "named"
	case claimproducer.ClaimSpec:
		return "scope"
	}
	return "zzz"
}

// sortKeyForSpec computes the sort key for a spec (blessed-invariant 3).
func sortKeyForSpec(sp any) string {
	switch v := sp.(type) {
	case locks.NamedLockSpec:
		return v.Name
	case claimproducer.ClaimSpec:
		return v.ProducerName + ":" + v.Selector
	}
	return ""
}

// producerNameForSpec returns the producer name for ClaimSpec, or "" for
// NamedLockSpec. Used to populate the `producer_name` audit-log payload
// field on claim-kind events.
func producerNameForSpec(sp any) string {
	if v, ok := sp.(claimproducer.ClaimSpec); ok {
		return v.ProducerName
	}
	return ""
}

// buildLockSpecs translates the template's per-node-type Stores+Locks
// declarations into concrete spec values. Substitutes `{{params.x}}`,
// `{{nodes.<n>.attribute.<f>}}`, and
// `{{claim.<alias>.{address|scope|payload.<f>}}}` (when the alias has
// a live inherited claim) into the selector and named-lock name per
// the substitution grammar.
//
// All persistence reads share the caller's tx — passing nil here would
// self-deadlock against the SQLite driver's single-connection pool
// (the tx holds the only conn; a nil-tx auto-commit would block
// forever). See foundation/persistence/sqlite/deadlock_guard_test.go.
func buildLockSpecs(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nd *persistence.NodeRow, def *node.TemplateNodeDef, inst *persistence.InstanceRow,
	dispatchID, frameID shared.UUID,
) ([]any, error) {
	if def == nil {
		return nil, nil
	}
	var paramsRaw json.RawMessage
	if inst != nil && len(inst.Params) > 0 {
		b, err := json.Marshal(inst.Params)
		if err != nil {
			return nil, err
		}
		paramsRaw = b
	}
	// Per-run keying (2026-05-20): substitution context comes from the
	// drained wait-set rows for this receiver in this frame. The
	// lock-substitution path runs at acquisition phase, but by then the
	// wait-set is settled (rows drained) — that's what made the receiver
	// eligible. The same builder works for both phases.
	deps, err := BuildAttributeDeps(ctx, tx, args, dispatchID, frameID)
	if err != nil {
		return nil, err
	}
	resolveCtx := attributes.ResolveContext{
		Params: paramsRaw,
		Deps:   deps,
		Claim:  loadInheritedClaimsForNode(ctx, args, tx, nd),
	}

	out := make([]any, 0, len(def.Locks)+len(def.Stores))
	for _, l := range def.Locks {
		nameSub, err := attributes.Substitute(l.Name, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, locks.NamedLockSpec{Name: nameSub})
	}
	for _, sref := range def.Stores {
		selectorSub, err := attributes.Substitute(sref.Selector, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, claimproducer.ClaimSpec{
			ProducerName: sref.Name,
			Selector:     selectorSub,
			Intent:       claimproducer.Intent(sref.Intent),
			Alias:        sref.AliasOf(),
			TemplateID:   instTemplateScope(inst),
			InstanceID:   instInstanceScope(inst),
		})
	}
	return out, nil
}

// loadInheritedClaimsForNode resolves the per-alias `{{claim.<alias>}}`
// substitution context for an inheritor / co-holder at acquire-time.
// Returns the upstream's `ClaimResult` (address + scope bytes) per
// alias, sourced from `rimsky_claim_handles` rows the upstream nodes
// hold.
//
// Resolution sources, in order of preference:
//
//  1. `holds:` declarations on the node's template (spec §Claim
//     co-holdership) — each entry names an upstream node-alias; the
//     upstream's claim handle for the matching alias is the source of
//     the address bytes.
//  2. `inherits:` declarations on the node's template (legacy,
//     pre-co-holdership) — each entry names a claim alias; the upstream
//     acquirer is resolved via `HoldingSubgraphsForTemplate`.
//
// Pre-stage-5 the runtime joined through `rimsky_claim_holders` rows
// the supervisor eagerly inserted at acquire-time of the acquirer; the
// post-stage-5 model defers the holder INSERT to the inheritor's own
// dispatch time, so the lookup now starts from the template directive
// and walks to the upstream's `rimsky_claim_handles` row directly.
//
// Reuses the caller's tx (option C / no-nil-tx). See buildLockSpecs.
func loadInheritedClaimsForNode(ctx context.Context, args RunArgs, tx persistence.Tx, nd *persistence.NodeRow) map[string]claimproducer.ClaimResult {
	if nd == nil {
		return nil
	}
	inst, err := args.Persist.Instances().Get(ctx, nd.InstanceID, tx)
	if err != nil || inst == nil {
		return nil
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil || tmpl == nil {
		return nil
	}
	nodeDef := lookupNodeDef(&tmpl.Spec, nd.NodeType)
	if nodeDef == nil {
		return nil
	}
	// Fast path: nodes with neither `holds:` nor `inherits:` skip the
	// per-binding lookup entirely. Avoids a ListByInstance + per-handle
	// roundtrip on every acquire of a non-co-holding node.
	if len(nodeDef.Holds) == 0 && len(nodeDef.Inherits) == 0 {
		return nil
	}
	out := map[string]claimproducer.ClaimResult{}
	collectCoHeldClaims(ctx, args, tx, &tmpl.Spec, nd.InstanceID, nodeDef, out)
	collectInheritedClaims(ctx, args, tx, &tmpl.Spec, nd.InstanceID, nodeDef, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectCoHeldClaims walks the node's `holds:` block and populates
// `out` with the upstream claim handle's address for each declared
// co-holdership. Silently skips entries that don't resolve (the
// upstream's acquire has not landed yet, or the upstream's template
// node is missing).
//
//	@concept: claim-co-holdership
func collectCoHeldClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec *node.TemplateSpec, instanceID shared.UUID,
	nodeDef *node.TemplateNodeDef, out map[string]claimproducer.ClaimResult,
) {
	if nodeDef == nil || len(nodeDef.Holds) == 0 {
		return
	}
	for alias, binding := range nodeDef.Holds {
		upstreamType := binding.From
		if upstreamType == "" {
			continue
		}
		// Find the upstream node within this instance.
		upstreamNode := findInstanceNodeByType(ctx, args, tx, instanceID, upstreamType)
		if upstreamNode == nil {
			continue
		}
		// Find the upstream's claim handle for this alias.
		lh := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, spec, upstreamType, alias)
		if lh == nil {
			continue
		}
		localAlias := alias
		if binding.As != "" {
			localAlias = binding.As
		}
		out[localAlias] = claimproducer.ClaimResult{
			Address:    lh.Address,
			ClaimScope: lh.ClaimScopeData,
		}
	}
}

// collectInheritedClaims walks the node's `inherits:` block (legacy
// pre-co-holdership path) and populates `out` with the upstream
// acquirer's claim handle address. The acquirer is resolved via the
// holding-subgraph computation that ValidateInheritance ran at deploy.
func collectInheritedClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec *node.TemplateSpec, instanceID shared.UUID,
	nodeDef *node.TemplateNodeDef, out map[string]claimproducer.ClaimResult,
) {
	if nodeDef == nil || len(nodeDef.Inherits) == 0 {
		return
	}
	subgraphs := node.HoldingSubgraphsForTemplate(spec)
	for _, ie := range nodeDef.Inherits {
		alias := ie.Claim
		if alias == "" {
			continue
		}
		if _, alreadyResolved := out[alias]; alreadyResolved {
			continue
		}
		// Find the acquirer node-type for this (member, alias) pair.
		var acquirerType string
		for _, sg := range subgraphs {
			if sg.Alias != alias {
				continue
			}
			if !memberOf(sg, nodeDef.Type) {
				continue
			}
			if sg.AcquirerType == nodeDef.Type {
				continue
			}
			acquirerType = sg.AcquirerType
			break
		}
		if acquirerType == "" {
			continue
		}
		upstreamNode := findInstanceNodeByType(ctx, args, tx, instanceID, acquirerType)
		if upstreamNode == nil {
			continue
		}
		lh := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, spec, acquirerType, alias)
		if lh == nil {
			continue
		}
		out[alias] = claimproducer.ClaimResult{
			Address:    lh.Address,
			ClaimScope: lh.ClaimScopeData,
		}
	}
}

// findInstanceNodeByType returns the rimsky_nodes row in the instance
// whose NodeType matches `t`. Returns nil when the instance does not
// declare that type (the canonicalizer flattens sub-graphs into a
// single instance topology; per-node lookup is by type).
func findInstanceNodeByType(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	instanceID shared.UUID, t string,
) *persistence.NodeRow {
	rows, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return nil
	}
	for i := range rows {
		if rows[i].NodeType == t {
			return &rows[i]
		}
	}
	return nil
}

// lookupClaimHandleForAlias finds the most recent rimsky_claim_handles
// row anchored on the upstream node for the named alias. Multiple aliases
// against the same producer_name disambiguate via the selector substitution
// pass (aliasFromAcquirerStores' inverse).
func lookupClaimHandleForAlias(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	upstreamNodeID shared.UUID, tmplSpec *node.TemplateSpec,
	upstreamType string, alias string,
) *persistence.ClaimHandleRow {
	upstreamDef := lookupNodeDef(tmplSpec, upstreamType)
	if upstreamDef == nil {
		return nil
	}
	var producerName string
	for _, sref := range upstreamDef.Stores {
		if sref.AliasOf() == alias {
			producerName = sref.Name
			break
		}
	}
	if producerName == "" {
		return nil
	}
	handles, err := args.ClaimHandles.ListByHolderNode(ctx, upstreamNodeID, tx)
	if err != nil || len(handles) == 0 {
		return nil
	}
	var best *persistence.ClaimHandleRow
	for i := range handles {
		h := &handles[i]
		if h.ProducerName == nil || *h.ProducerName != producerName {
			continue
		}
		// Prefer the most recently claimed row.
		if best == nil || h.ClaimedAt.After(best.ClaimedAt) {
			best = h
		}
	}
	return best
}

// memberOf for holding-subgraph membership lookup lives in
// runner_held_claims.go.
//
// (The pre-stage-5 `aliasFromAcquirerStores` helper was retired by the
// rewrite of `loadInheritedClaimsForNode`: the post-stage-5 lookup
// starts from the template `holds:` / `inherits:` directive and walks
// to the upstream claim handle directly, so there's no need to invert
// from a holder row's producer_name back to an alias.)

// instTemplateScope returns the template-scope id sent to the store on
// Open. Per docs/specs/2026-05-01-control-plane-and-store-lifecycle-
// design.md §4.2: the supervisor populates this from the dispatch row's
// instance → template lookup. Returns the empty string when the
// instance row is unavailable; the store treats empty as scope-absent.
func instTemplateScope(inst *persistence.InstanceRow) string {
	if inst == nil {
		return ""
	}
	return inst.TemplateHash
}

// instInstanceScope returns the instance-scope id (the rimsky-generated
// instance UUID) sent to the store on Open.
func instInstanceScope(inst *persistence.InstanceRow) string {
	if inst == nil {
		return ""
	}
	return inst.ID.String()
}

// lookupTemplate fetches the template for an instance, or nil on miss.
// Reuses the caller's tx (option C / no-nil-tx). See buildLockSpecs.
func lookupTemplate(ctx context.Context, args RunArgs, tx persistence.Tx, inst *persistence.InstanceRow) *node.TemplateSpec {
	if inst == nil {
		return nil
	}
	tmpl, _ := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if tmpl == nil {
		return nil
	}
	return &tmpl.Spec
}

// templateAttributeDefaultsFor returns the already-routed by-executor
// attribute fragment from the bound template's
// `Defaults.Attributes.ByExecutor[executor]`. Returns nil when the
// template, defaults, or per-executor entry is absent. The runtime
// path threads this through `acquisition.TemplateAttributeDefaults`
// onto `computeEffectiveAttributeSchema` as the L1 layer merged into
// the effective schema at dispatch.
//
// @concept: attribute
func templateAttributeDefaultsFor(tmpl *node.TemplateSpec, executor string) map[string]any {
	if tmpl == nil || tmpl.Defaults == nil || tmpl.Defaults.Attributes == nil {
		return nil
	}
	frag, ok := tmpl.Defaults.Attributes.ByExecutor[executor]
	if !ok {
		return nil
	}
	return frag
}

// lookupNodeDef returns the per-node-type def from a template, or nil.
func lookupNodeDef(tmpl *node.TemplateSpec, nodeType string) *node.TemplateNodeDef {
	if tmpl == nil {
		return nil
	}
	for i := range tmpl.Nodes {
		if tmpl.Nodes[i].Type == nodeType {
			return &tmpl.Nodes[i]
		}
	}
	return nil
}
