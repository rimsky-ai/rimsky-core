// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func sortLockSpecs(specs []any) {
	sort.SliceStable(specs, func(i, j int) bool {
		ki, kj := kindForSpec(specs[i]), kindForSpec(specs[j])
		if ki != kj {
			return ki < kj
		}
		return sortKeyForSpec(specs[i]) < sortKeyForSpec(specs[j])
	})
}

func kindForSpec(sp any) string {
	switch sp.(type) {
	case locks.NamedLockSpec:
		return "named"
	case claimproducer.ClaimSpec:
		return "scope"
	}
	return "zzz"
}

func sortKeyForSpec(sp any) string {
	switch v := sp.(type) {
	case locks.NamedLockSpec:
		return v.Name
	case claimproducer.ClaimSpec:
		return v.ProducerName + ":" + v.Selector
	}
	return ""
}

func producerNameForSpec(sp any) string {
	if v, ok := sp.(claimproducer.ClaimSpec); ok {
		return v.ProducerName
	}
	return ""
}

func buildLockSpecs(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nd *persistence.NodeRow, def *node.TemplateNodeDef, inst *persistence.InstanceRow,
	nodeRunID, frameID, runScopeID shared.UUID,
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
	deps, err := BuildAttributeDeps(ctx, tx, args, nodeRunID, frameID)
	if err != nil {
		return nil, err
	}
	var templateHash string
	if inst != nil {
		templateHash = inst.TemplateHash
	}
	registryTypes := declaredMessageTypesForTemplate(ctx, args, templateHash, tx)
	resolveCtx := attributes.ResolveContext{
		Params:                paramsRaw,
		Deps:                  deps,
		Claim:                 loadInheritedClaimsForNode(ctx, args, tx, nd),
		RegistryDeclaredTypes: registryTypes,
	}

	out := make([]any, 0, len(def.Locks)+len(def.ClaimProducers))
	for _, l := range def.Locks {
		nameSub, err := attributes.Substitute(l.Name, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, locks.NamedLockSpec{Name: nameSub, TemplateName: l.Name})
	}
	for _, sref := range def.ClaimProducers {
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
			// @concept: host-agent-proxy
			RunScopeID: runScopeIDString(runScopeID),
			// @concept: claim-lifetime
			Lifetime: sref.Lifetime,
		})
	}
	return out, nil
}

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
	for _, sref := range upstreamDef.ClaimProducers {
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

func instTemplateScope(inst *persistence.InstanceRow) string {
	if inst == nil {
		return ""
	}
	return inst.TemplateHash
}

func instInstanceScope(inst *persistence.InstanceRow) string {
	if inst == nil {
		return ""
	}
	return inst.ID.String()
}

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
