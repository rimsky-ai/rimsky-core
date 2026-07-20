// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: advisory-lock
func sortLockSpecs(specs []any) {
	sort.SliceStable(specs, func(i, j int) bool {
		ki, kj := kindForSpec(specs[i]), kindForSpec(specs[j])
		if ki != kj {
			return ki < kj
		}
		return sortKeyForSpec(specs[i]) < sortKeyForSpec(specs[j])
	})
}

// @concept: named-lock
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
	heldClaims, err := loadInheritedClaimsForNode(ctx, args, tx, nd, frameID)
	if err != nil {
		return nil, fmt.Errorf("buildLockSpecs: load inherited claims: %w", err)
	}
	resolveCtx := attributes.ResolveContext{
		Params:                paramsRaw,
		Deps:                  deps,
		Claim:                 heldClaims,
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

func loadInheritedClaimsForNode(
	ctx context.Context, args RunArgs, tx persistence.Tx, nd *persistence.NodeRow, frameID shared.UUID,
) (map[string]claimproducer.ClaimResult, error) {
	if nd == nil {
		return nil, nil
	}
	inst, err := args.Persist.Instances().Get(ctx, nd.InstanceID, tx)
	if err != nil {
		return nil, fmt.Errorf("loadInheritedClaimsForNode: Instances.Get: %w", err)
	}
	if inst == nil {
		return nil, nil
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("loadInheritedClaimsForNode: Templates.GetByHash: %w", err)
	}
	if tmpl == nil {
		return nil, nil
	}
	nodeDef := LookupNodeDef(&tmpl.Spec, nd.NodeType)
	if nodeDef == nil {
		return nil, nil
	}
	if len(nodeDef.Holds) == 0 {
		return nil, nil
	}
	out := map[string]claimproducer.ClaimResult{}
	if err := collectCoHeldClaims(ctx, args, tx, &tmpl.Spec, nd.InstanceID, nodeDef, out, frameID); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// @concept: claim-co-holdership
func collectCoHeldClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	tmplSpec *node.TemplateSpec, instanceID shared.UUID,
	nodeDef *node.TemplateNodeDef, out map[string]claimproducer.ClaimResult, frameID shared.UUID,
) error {
	if nodeDef == nil || len(nodeDef.Holds) == 0 {
		return nil
	}
	for alias, binding := range nodeDef.Holds {
		upstreamType := binding.From
		if upstreamType == "" {
			continue
		}
		upstreamNode, err := findInstanceNodeByType(ctx, args, tx, instanceID, upstreamType)
		if err != nil {
			return fmt.Errorf("collectCoHeldClaims: lookup upstream node (alias %q): %w", alias, err)
		}
		if upstreamNode == nil {
			continue
		}
		lh, err := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, tmplSpec, upstreamType, alias, frameID)
		if err != nil {
			return fmt.Errorf("collectCoHeldClaims: lookup claim handle (alias %q): %w", alias, err)
		}
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
	return nil
}

func findInstanceNodeByType(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	instanceID shared.UUID, t string,
) (*persistence.NodeRow, error) {
	rows, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return nil, fmt.Errorf("findInstanceNodeByType: ListByInstance: %w", err)
	}
	for i := range rows {
		if rows[i].NodeType == t {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// @concept: claim-co-holdership
func lookupClaimHandleForAlias(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	upstreamNodeID shared.UUID, tmplSpec *node.TemplateSpec,
	upstreamType string, alias string, frameID shared.UUID,
) (*persistence.ClaimHandleRow, error) {
	upstreamDef := LookupNodeDef(tmplSpec, upstreamType)
	if upstreamDef == nil {
		return nil, nil
	}
	var producerName string
	for _, sref := range upstreamDef.ClaimProducers {
		if sref.AliasOf() == alias {
			producerName = sref.Name
			break
		}
	}
	if producerName == "" {
		return nil, nil
	}
	handles, err := args.ClaimHandles.ListByHolderNode(ctx, upstreamNodeID, tx)
	if err != nil {
		return nil, fmt.Errorf("lookupClaimHandleForAlias: ListByHolderNode: %w", err)
	}
	var best *persistence.ClaimHandleRow
	for i := range handles {
		h := &handles[i]
		if h.ProducerName == nil || *h.ProducerName != producerName {
			continue
		}
		if h.State != spec.ClaimHandleStateActive && h.State != spec.ClaimHandleStateCommitted {
			continue
		}
		if h.Lifetime != spec.ClaimLifetimeDurable && (h.FrameID == nil || *h.FrameID != frameID) {
			continue
		}
		if best == nil || h.ClaimedAt.After(best.ClaimedAt) {
			best = h
		}
	}
	return best, nil
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

func LookupNodeDef(tmpl *node.TemplateSpec, nodeType string) *node.TemplateNodeDef {
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
