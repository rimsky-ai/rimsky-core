// Lock-spec construction + template lookup helpers.
//
// Two primitives, two types: store.NamedLockSpec and store.ClaimSpec.
// There is no LockSpec interface; this file's helpers operate on `any`
// values and dispatch by type-switch.
//
// Spec §13.7 deterministic ordering: (kind, sort_key) with
// "named" < "region" and:
//   - NamedLockSpec sort key: Name
//   - ClaimSpec sort key:     StoreName + ":" + Selector (post-substitution)

package supervisor

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/fallguy/rimsky/core/attributes"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// sortLockSpecs orders specs by (kind, sort_key) per §13.7. Inputs may
// be NamedLockSpec or ClaimSpec values; unknown types sort last.
func sortLockSpecs(specs []any) {
	sort.SliceStable(specs, func(i, j int) bool {
		ki, kj := kindForSpec(specs[i]), kindForSpec(specs[j])
		if ki != kj {
			return ki < kj
		}
		return sortKeyForSpec(specs[i]) < sortKeyForSpec(specs[j])
	})
}

// kindForSpec returns the §13.7 kind tag for a spec value.
// "named" < "region"; the lexical ordering matches the spec table.
func kindForSpec(sp any) string {
	switch sp.(type) {
	case store.NamedLockSpec:
		return "named"
	case store.ClaimSpec:
		return "region"
	}
	return "zzz"
}

// sortKeyForSpec computes the §13.7 sort key for a spec.
func sortKeyForSpec(sp any) string {
	switch v := sp.(type) {
	case store.NamedLockSpec:
		return v.Name
	case store.ClaimSpec:
		return v.StoreName + ":" + v.Selector
	}
	return ""
}

// storeNameForSpec returns the store name for ClaimSpec, or "" for
// NamedLockSpec.
func storeNameForSpec(sp any) string {
	if v, ok := sp.(store.ClaimSpec); ok {
		return v.StoreName
	}
	return ""
}

// buildLockSpecs translates the template's per-node-type Stores+Locks
// declarations into concrete spec values. Substitutes `{{params.x}}`,
// `{{deps.<n>.<f>}}`, and `{{claim.<alias>.{address|region|payload.<f>}}}`
// (when the alias has a live inherited claim) into the selector and
// named-lock name per spec §16.5.
func buildLockSpecs(
	ctx context.Context, args RunArgs,
	nd *storage.NodeRow, def *node.TemplateNodeDef, inst *storage.InstanceRow,
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
	resolveCtx := attributes.ResolveContext{
		Params: paramsRaw,
		Deps:   loadDepsAttributes(ctx, args, nd),
		Claim:  loadInheritedClaimsForNode(ctx, args, nd),
	}

	out := make([]any, 0, len(def.Locks)+len(def.Stores))
	for _, l := range def.Locks {
		nameSub, err := attributes.Substitute(l.Name, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, store.NamedLockSpec{Name: nameSub})
	}
	for _, sref := range def.Stores {
		selectorSub, err := attributes.Substitute(sref.Selector, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, store.ClaimSpec{
			StoreName: sref.Name,
			Selector:  selectorSub,
			Intent:    store.Intent(sref.Intent),
			Alias:     sref.AliasOf(),
		})
	}
	return out, nil
}

// loadInheritedClaimsForNode reads this node's active rimsky_claim_holders
// rows, joins each to its lock-holder, and returns a per-alias
// store.ClaimResult map suitable for `{{claim.<alias>.…}}` substitution.
// Aliases for which this node is itself the acquirer are resolved from
// the lock-holder rows the supervisor wrote at acquire time. Returns nil
// when the node holds no claim-holder rows.
func loadInheritedClaimsForNode(ctx context.Context, args RunArgs, nd *storage.NodeRow) map[string]store.ClaimResult {
	if nd == nil {
		return nil
	}
	rows, err := args.Storage.ClaimHolders().ListByHolderNode(ctx, nd.ID, nil)
	if err != nil || len(rows) == 0 {
		return nil
	}
	inst, err := args.Storage.Instances().Get(ctx, nd.InstanceID, nil)
	if err != nil || inst == nil {
		return nil
	}
	tmpl, err := args.Storage.Templates().Get(ctx, inst.TemplateID, nil)
	if err != nil || tmpl == nil {
		return nil
	}
	out := map[string]store.ClaimResult{}
	for _, r := range rows {
		lh, err := args.Storage.LockHolders().Get(ctx, r.LockHolderID, nil)
		if err != nil || lh == nil {
			continue
		}
		acquirer, err := args.Storage.Nodes().Get(ctx, lh.HolderNodeID, nil)
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
		out[alias] = store.ClaimResult{
			Address: lh.Address,
			Region:  lh.RegionData,
		}
	}
	return out
}

// aliasFromAcquirerStores resolves the alias-name on the acquirer
// NodeDef whose store_name matches the lock-holder row, preferring an
// alias whose substituted selector matches the row's region_data.
func aliasFromAcquirerStores(def *node.TemplateNodeDef, lh *storage.LockHolderRow) string {
	if def == nil || lh == nil || lh.StoreName == nil {
		return ""
	}
	storeName := *lh.StoreName
	candidates := make([]node.NodeStoreRef, 0, len(def.Stores))
	for _, sref := range def.Stores {
		if sref.Name == storeName {
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
		if string(lh.RegionData) == string(encoded) {
			return sref.AliasOf()
		}
	}
	return candidates[0].AliasOf()
}

// loadDepsAttributes pulls each upstream node's
// rimsky_node_attributes.data into a map keyed by the upstream's
// node_type, marshalled to json.RawMessage so the substitution engine
// can lazy-walk into it.
func loadDepsAttributes(ctx context.Context, args RunArgs, nd *storage.NodeRow) map[string]json.RawMessage {
	if nd == nil || len(nd.Dependencies) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(nd.Dependencies))
	for _, depID := range nd.Dependencies {
		depNode, _ := args.Storage.Nodes().Get(ctx, depID, nil)
		if depNode == nil {
			continue
		}
		row, err := args.Storage.NodeAttributes().Get(ctx, depNode.ID)
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

// lookupTemplate fetches the template for an instance, or nil on miss.
func lookupTemplate(ctx context.Context, args RunArgs, inst *storage.InstanceRow) *node.TemplateSpec {
	if inst == nil {
		return nil
	}
	tmpl, _ := args.Storage.Templates().Get(ctx, inst.TemplateID, nil)
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
