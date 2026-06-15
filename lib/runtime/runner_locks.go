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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
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
// `{{claim.<alias>.{address|claim_scope|payload.<f>}}}` (when the alias has
// a live co-held claim) into the selector and named-lock name per
// the substitution grammar.
//
// All persistence reads share the caller's tx — passing nil here would
// self-deadlock against the SQLite driver's single-connection pool
// (the tx holds the only conn; a nil-tx auto-commit would block
// forever). See foundation/persistence/sqlite/deadlock_guard_test.go.
// runScopeID is the RunScope this acquisition lives in (computed at the
// acquire site from the run-tree row — it is NOT in scope here otherwise,
// unlike InstanceID which derives from inst). It is stamped onto each
// ClaimSpec.RunScopeID so the claim-producer Open path can carry it to the
// host-agent-proxy for per-run-scope spawn isolation. Zero-valued for the
// degenerate / non-fanned-out path; the proxy falls back to instance keying.
func buildLockSpecs(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nd *persistence.NodeRow, def *node.TemplateNodeDef, inst *persistence.InstanceRow,
	dispatchID, frameID, runScopeID shared.UUID,
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
	// @deliberate: substitution context comes from the drained wait-set rows
	// for this receiver in this frame. The lock-substitution path runs at
	// acquisition phase, but by then the wait-set is settled (rows drained) —
	// that's what made the receiver eligible. The same builder works for both
	// phases.
	deps, err := BuildAttributeDeps(ctx, tx, args, dispatchID, frameID)
	if err != nil {
		return nil, err
	}
	// Bind the frame's triggering message so the lock-name substitution
	// path admits `{{trigger.message.payload.X}}` and the new
	// `{{messages.<type>.<field>}}` arm uniformly with dispatch-time
	// substitution. One resolver function services both directive
	// shapes (per spec §Load-bearing property "one substitution engine,
	// two surfaces").
	triggerPayload, triggerType := triggerMessageForFrame(ctx, args, tx, frameID)
	// Defense-in-depth: thread the template's declared message-type set
	// so `{{messages.<type>.<field>}}` references against undeclared
	// types fail with ErrMissingSource even on lock-name substitution.
	// Mirrors `buildResolveContextForDispatch` in runner_dispatch.go.
	var templateHash string
	if inst != nil {
		templateHash = inst.TemplateHash
	}
	registryTypes := declaredMessageTypesForTemplate(ctx, args, templateHash, tx)
	resolveCtx := attributes.ResolveContext{
		Params:                paramsRaw,
		Deps:                  deps,
		Claim:                 loadInheritedClaimsForNode(ctx, args, tx, nd),
		TriggerMessagePayload: triggerPayload,
		TriggerMessageType:    triggerType,
		RegistryDeclaredTypes: registryTypes,
	}

	out := make([]any, 0, len(def.Locks)+len(def.Stores))
	for _, l := range def.Locks {
		nameSub, err := attributes.Substitute(l.Name, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, locks.NamedLockSpec{Name: nameSub, TemplateName: l.Name})
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
			// @constraint: carry the run-scope onto the OpenRequest so the
			// host-agent-proxy keys per-run-scope spawn isolation on the
			// claim-producer path too. Empty for the zero/degenerate
			// run-scope (proxy falls back to instance keying).
			// @concept: host-agent-proxy
			RunScopeID: runScopeIDString(runScopeID),
			// @constraint: carry the template store-ref's lifetime hint
			// ("subgraph" / "durable") through to the persistence boundary.
			// NodeStoreRef.Lifetime is a plain string; acquireClaim converts
			// it to spec.ClaimLifetime at the ClaimHandleInsertInput.
			// @concept: claim-lifetime
			Lifetime: sref.Lifetime,
		})
	}
	return out, nil
}

// loadInheritedClaimsForNode resolves the per-alias `{{claim.<alias>}}`
// substitution context for a co-holder at acquire-time. Returns the
// upstream's `ClaimResult` (address + scope bytes) per alias, sourced
// from `rimsky_claim_handles` rows the upstream nodes hold.
//
// Resolution source: `holds:` declarations on the node's template (spec
// §Claim co-holdership) — each entry names an upstream node-alias; the
// upstream's claim handle for the matching alias is the source of the
// address bytes.
//
// Pre-stage-5 the runtime joined through `rimsky_claim_holders` rows
// the supervisor eagerly inserted at acquire-time of the acquirer; the
// post-stage-5 model defers the holder INSERT to the co-holder's own
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
	// @deliberate: nodes without `holds:` skip the per-binding lookup
	// entirely. Avoids a ListByInstance + per-handle roundtrip on every
	// acquire of a non-co-holding node.
	if len(nodeDef.Holds) == 0 {
		return nil
	}
	out := map[string]claimproducer.ClaimResult{}
	collectCoHeldClaims(ctx, args, tx, &tmpl.Spec, nd.InstanceID, nodeDef, out)
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
// @concept: claim-co-holdership
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
		upstreamNode := findInstanceNodeByType(ctx, args, tx, instanceID, upstreamType)
		if upstreamNode == nil {
			continue
		}
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
			Payload:    lh.Payload,
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
		if best == nil || h.ClaimedAt.After(best.ClaimedAt) {
			best = h
		}
	}
	return best
}

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
