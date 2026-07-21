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
	ctx context.Context, args RunArgs, nd *persistence.NodeRow, def *node.TemplateNodeDef, tmpl *node.TemplateSpec, inst *persistence.InstanceRow, nodeRunID, frameID, runScopeID shared.UUID, tx persistence.Tx,
) ([]any, map[string]claimproducer.ClaimResult, error) {
	if def == nil {
		return nil, nil, nil
	}
	var paramsRaw json.RawMessage
	if inst != nil && len(inst.Params) > 0 {
		b, err := json.Marshal(inst.Params)
		if err != nil {
			return nil, nil, err
		}
		paramsRaw = b
	}
	var templateHash string
	if inst != nil {
		templateHash = inst.TemplateHash
	}
	var instanceID shared.UUID
	if nd != nil {
		instanceID = nd.InstanceID
	}
	heldClaims, err := loadInheritedClaimsForNode(ctx, args, instanceID, tmpl, def, frameID, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("buildLockSpecs: load inherited claims: %w", err)
	}
	resolveCtx, err := buildResolveContextForAcquisition(ctx, args, frameID, nodeRunID, templateHash, paramsRaw, heldClaims, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("buildLockSpecs: build resolve context: %w", err)
	}

	out := make([]any, 0, len(def.Locks)+len(def.ClaimProducers))
	for _, l := range def.Locks {
		nameSub, err := attributes.Substitute(l.Name, resolveCtx)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, locks.NamedLockSpec{Name: nameSub, TemplateName: l.Name})
	}
	for _, sref := range def.ClaimProducers {
		selectorSub, err := attributes.Substitute(sref.Selector, resolveCtx)
		if err != nil {
			return nil, nil, err
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
	return out, heldClaims, nil
}

func loadInheritedClaimsForNode(
	ctx context.Context, args RunArgs, instanceID shared.UUID, tmplSpec *node.TemplateSpec, nodeDef *node.TemplateNodeDef, frameID shared.UUID, tx persistence.Tx,
) (map[string]claimproducer.ClaimResult, error) {
	if tmplSpec == nil || nodeDef == nil {
		return nil, nil
	}
	if len(nodeDef.Holds) == 0 {
		return nil, nil
	}
	out := map[string]claimproducer.ClaimResult{}
	if err := collectCoHeldClaims(ctx, args, tmplSpec, instanceID, nodeDef, out, frameID, tx); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

type resolvedHold struct {
	alias        string
	binding      node.HoldsBinding
	upstreamNode *persistence.NodeRow
	claimHandle  *persistence.ClaimHandleRow
}

// @concept: claim-co-holdership
func resolveHolds(
	ctx context.Context, args RunArgs, instanceID shared.UUID, tmplSpec *node.TemplateSpec, nodeDef *node.TemplateNodeDef, frameID shared.UUID, requireUpstreamNode bool, tx persistence.Tx,
) ([]resolvedHold, error) {
	if nodeDef == nil || len(nodeDef.Holds) == 0 {
		return nil, nil
	}
	var out []resolvedHold
	for alias, binding := range nodeDef.Holds {
		upstreamType := binding.From
		if upstreamType == "" {
			continue
		}
		upstreamNode, err := findInstanceNodeByType(ctx, args, instanceID, upstreamType, tx)
		if err != nil {
			return nil, fmt.Errorf("resolveHolds: find upstream node (alias %q): %w", alias, err)
		}
		if upstreamNode == nil {
			if requireUpstreamNode {
				return nil, fmt.Errorf("resolveHolds: upstream node-type %q not found in instance %s (alias %q)",
					upstreamType, instanceID.String(), alias)
			}
			continue
		}
		lh, err := lookupClaimHandleForAlias(ctx, args, upstreamNode.ID, tmplSpec, upstreamType, alias, frameID, tx)
		if err != nil {
			return nil, fmt.Errorf("resolveHolds: lookup claim handle (alias %q): %w", alias, err)
		}
		if lh == nil {
			continue
		}
		out = append(out, resolvedHold{alias: alias, binding: binding, upstreamNode: upstreamNode, claimHandle: lh})
	}
	return out, nil
}

// @concept: claim-co-holdership
func collectCoHeldClaims(
	ctx context.Context, args RunArgs, tmplSpec *node.TemplateSpec, instanceID shared.UUID, nodeDef *node.TemplateNodeDef, out map[string]claimproducer.ClaimResult, frameID shared.UUID, tx persistence.Tx,
) error {
	resolved, err := resolveHolds(ctx, args, instanceID, tmplSpec, nodeDef, frameID, false, tx)
	if err != nil {
		return fmt.Errorf("collectCoHeldClaims: %w", err)
	}
	for _, r := range resolved {
		localAlias := node.EffectiveHoldsLocalAlias(r.alias, r.binding)
		out[localAlias] = claimproducer.ClaimResult{
			Address:    r.claimHandle.Address,
			Payload:    r.claimHandle.Payload,
			ClaimScope: r.claimHandle.ClaimScopeData,
		}
	}
	return nil
}

func findInstanceNodeByType(
	ctx context.Context, args RunArgs, instanceID shared.UUID, t string, tx persistence.Tx,
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
	ctx context.Context, args RunArgs, upstreamNodeID shared.UUID, tmplSpec *node.TemplateSpec, upstreamType string, alias string, frameID shared.UUID, tx persistence.Tx,
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

func lookupTemplate(ctx context.Context, args RunArgs, inst *persistence.InstanceRow, tx persistence.Tx) *node.TemplateSpec {
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
