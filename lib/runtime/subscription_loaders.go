// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: node-subscription
// @concept: wait-set
package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

var templateSubscriptionEdges sync.Map

func subscriptionEdgesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) (*node.SubscriptionEdgeMap, error) {
	if v, ok := templateSubscriptionEdges.Load(templateHash); ok {
		return v.(*node.SubscriptionEdgeMap), nil
	}
	row, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: get template %s: %w", templateHash, err)
	}
	if row == nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: template %s not found", templateHash)
	}
	subs := node.ExtractSubstitutionRefsFromTemplate(row.Spec)
	msgs := node.ExtractMessageRefsFromTemplate(row.Spec)
	edges, err := node.BuildSubscriptionEdges(row.Spec, subs, msgs)
	if err != nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: build edges for %s: %w", templateHash, err)
	}
	actual, _ := templateSubscriptionEdges.LoadOrStore(templateHash, edges)
	return actual.(*node.SubscriptionEdgeMap), nil
}

// @concept: message-schema
var templateDeclaredMessageTypes sync.Map

// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
func declaredMessageTypesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) map[string]struct{} {
	if templateHash == "" {
		return nil
	}
	if v, ok := templateDeclaredMessageTypes.Load(templateHash); ok {
		return v.(map[string]struct{})
	}
	if args.Persist == nil || args.Persist.Templates() == nil {
		return nil
	}
	row, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil || row == nil {
		return nil
	}
	set := make(map[string]struct{}, len(row.Spec.Messages)+1)
	for _, m := range row.Spec.Messages {
		set[m.Type] = struct{}{}
	}
	set[""] = struct{}{}
	actual, _ := templateDeclaredMessageTypes.LoadOrStore(templateHash, set)
	return actual.(map[string]struct{})
}

// @concept: cascade
// @concept: node-subscription
var templateHardDepEdges sync.Map

func hardDepEdgesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) (node.HardDepEdgeMap, error) {
	if v, ok := templateHardDepEdges.Load(templateHash); ok {
		return v.(node.HardDepEdgeMap), nil
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("hardDepEdgesForTemplate: get template %s: %w", templateHash, err)
	}
	if tmpl == nil {
		return nil, fmt.Errorf("hardDepEdgesForTemplate: template %s not found", templateHash)
	}
	edges, err := node.BuildHardDepEdges(tmpl.Spec)
	if err != nil {
		return nil, err
	}
	actual, _ := templateHardDepEdges.LoadOrStore(templateHash, edges)
	return actual.(node.HardDepEdgeMap), nil
}
