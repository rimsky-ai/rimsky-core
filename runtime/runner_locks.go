// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lock-spec construction + template lookup helpers.
//
// Two primitives, two types: locks.NamedLockSpec and locks.ClaimSpec.
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

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	attributes "github.com/fallguy/rimsky/graph/attribute"
	"github.com/fallguy/rimsky/graph/node"
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
	case locks.ClaimSpec:
		return "scope"
	}
	return "zzz"
}

// sortKeyForSpec computes the sort key for a spec (blessed-invariant 3).
func sortKeyForSpec(sp any) string {
	switch v := sp.(type) {
	case locks.NamedLockSpec:
		return v.Name
	case locks.ClaimSpec:
		return v.ProducerName + ":" + v.Selector
	}
	return ""
}

// producerNameForSpec returns the producer name for ClaimSpec, or "" for
// NamedLockSpec. Used to populate the `producer_name` audit-log payload
// field on claim-kind events.
func producerNameForSpec(sp any) string {
	if v, ok := sp.(locks.ClaimSpec); ok {
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
	var subs []shared.UUID
	if nd != nil {
		// Post-T23: subscribed-sender set resolved from the cached
		// per-template subscription-edge inverse map (see
		// runtime/subscription_loaders.go); the retired
		// nodes.dependencies column is no longer consulted.
		ss, sErr := resolveSubscribedSenders(ctx, args, nd.ID, tx)
		if sErr != nil {
			return nil, sErr
		}
		subs = ss
	}
	resolveCtx := attributes.ResolveContext{
		Params: paramsRaw,
		Deps:   loadSubscribedNodeAttributes(ctx, args, tx, subs),
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
		out = append(out, locks.ClaimSpec{
			ProducerName: sref.Name,
			Selector:     selectorSub,
			Intent:       locks.Intent(sref.Intent),
			Alias:        sref.AliasOf(),
			TemplateID:   instTemplateScope(inst),
			InstanceID:   instInstanceScope(inst),
		})
	}
	return out, nil
}

// loadInheritedClaimsForNode reads this node's active rimsky_claim_holders
// rows, joins each to its lock-holder, and returns a per-alias
// locks.ClaimResult map suitable for `{{claim.<alias>.…}}` substitution.
// Aliases for which this node is itself the acquirer are resolved from
// the lock-holder rows the supervisor wrote at acquire time. Returns nil
// when the node holds no claim-holder rows.
//
// Reuses the caller's tx (option C / no-nil-tx). See buildLockSpecs.
func loadInheritedClaimsForNode(ctx context.Context, args RunArgs, tx persistence.Tx, nd *persistence.NodeRow) map[string]locks.ClaimResult {
	if nd == nil {
		return nil
	}
	rows, err := args.Persist.ClaimHolders().ListByHolderNode(ctx, nd.ID, tx)
	if err != nil || len(rows) == 0 {
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
	out := map[string]locks.ClaimResult{}
	for _, r := range rows {
		lh, err := args.ClaimHandles.Get(ctx, r.ClaimHandleID, tx)
		if err != nil || lh == nil {
			continue
		}
		acquirer, err := args.Persist.Nodes().Get(ctx, lh.HolderNodeID, tx)
		if err != nil || acquirer == nil {
			continue
		}
		acqDef := lookupNodeDef(&tmpl.Spec, acquirer.NodeType)
		if acqDef == nil {
			continue
		}
		alias := aliasFromAcquirerStores(acqDef, lh)
		if alias == "" {
			continue
		}
		out[alias] = locks.ClaimResult{
			Address: lh.Address,
			Scope:   lh.ScopeData,
		}
	}
	return out
}

// aliasFromAcquirerStores resolves the alias-name on the acquirer
// NodeDef whose producer_name matches the lock-holder row, preferring an
// alias whose substituted selector matches the row's scope_data.
func aliasFromAcquirerStores(def *node.TemplateNodeDef, lh *persistence.ClaimHandleRow) string {
	if def == nil || lh == nil || lh.ProducerName == nil {
		return ""
	}
	producerName := *lh.ProducerName
	candidates := make([]node.NodeStoreRef, 0, len(def.Stores))
	for _, sref := range def.Stores {
		if sref.Name == producerName {
			candidates = append(candidates, sref)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0].AliasOf()
	}
	for _, sref := range candidates {
		encoded, err := json.Marshal(sref.Selector)
		if err != nil {
			continue
		}
		if string(lh.ScopeData) == string(encoded) {
			return sref.AliasOf()
		}
	}
	return candidates[0].AliasOf()
}

// loadSubscribedNodeAttributes pulls each subscribed-to upstream node's
// rimsky_node_attributes.data into a map keyed by the upstream's
// node_type, marshalled to json.RawMessage so the substitution engine
// can lazy-walk into it.
//
// `subscribedNodeIDs` is the set of upstream node UUIDs the receiver is
// subscribed to (either explicitly via Subscribes or implicitly via
// `{{nodes.X.attribute.Y}}` substitution refs). Post-T23: callers
// resolve this via runtime/subscription_loaders.go::resolveSubscribedSenders
// against the cached per-template subscription-edge inverse map.
//
// Reuses the caller's tx (option C / no-nil-tx). See buildLockSpecs.
//
//	@concept: subscription
func loadSubscribedNodeAttributes(ctx context.Context, args RunArgs, tx persistence.Tx, subscribedNodeIDs []shared.UUID) map[string]json.RawMessage {
	if len(subscribedNodeIDs) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(subscribedNodeIDs))
	for _, depID := range subscribedNodeIDs {
		depNode, _ := args.Persist.Nodes().Get(ctx, depID, tx)
		if depNode == nil {
			continue
		}
		row, err := args.Persist.NodeAttributes().Get(ctx, depNode.ID, tx)
		if err != nil || row == nil {
			continue
		}
		raw, err := json.Marshal(row.Data)
		if err != nil {
			continue
		}
		out[depNode.NodeType] = raw
	}
	return out
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
