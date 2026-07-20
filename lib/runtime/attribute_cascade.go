// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: cascade
// @concept: signal
// @concept: attribute

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func emitAttributeChangesForRunInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nodeID foundationshared.UUID, nodeType string,
	runID, instanceID, frameID foundationshared.UUID,
	visited map[foundationshared.UUID]struct{},
	filter receiverFilter,
) error {
	current, err := args.Persist.NodeAttributes().GetByRun(ctx, runID, tx)
	if err != nil {
		return fmt.Errorf("emitAttributeChangesForRunInTx: load current attrs: %w", err)
	}
	if current == nil {
		return nil
	}
	prior, err := args.Persist.NodeAttributes().GetPriorRunData(ctx, tx, runID)
	if err != nil {
		return fmt.Errorf("emitAttributeChangesForRunInTx: load prior attrs: %w", err)
	}
	if prior == nil {
		prior, err = firstRunAttributeBaseline(ctx, args, tx, instanceID, nodeType)
		if err != nil {
			return fmt.Errorf("emitAttributeChangesForRunInTx: first-run baseline: %w", err)
		}
	}
	changes := diffAttributesData(prior, current.Data)
	if len(changes) == 0 {
		return nil
	}
	if visited == nil {
		visited = map[foundationshared.UUID]struct{}{}
	}
	for key, value := range changes {
		attrSig := signalpkg.Signal{
			Type: signalpkg.TypePath(fmt.Sprintf("attribute/%s/changed", key)),
			Payload: map[string]any{
				"key":   key,
				"value": value,
			},
		}
		if err := emitSignalInTxWithFilter(ctx, args, tx,
			nodeID, nodeType, runID, instanceID, frameID,
			attrSig, visited, filter); err != nil {
			return err
		}
	}
	return nil
}

func firstRunAttributeBaseline(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	instanceID foundationshared.UUID, nodeType string,
) (map[string]any, error) {
	tmpl, err := loadTemplateSpec(ctx, args, tx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("firstRunAttributeBaseline: load template: %w", err)
	}
	nodeDef := LookupNodeDef(tmpl, nodeType)
	if nodeDef == nil {
		return map[string]any{}, nil
	}
	var nodeSchema map[string]any
	if nodeDef.Attributes != nil {
		nodeSchema = nodeDef.Attributes.Schema
	}
	var execSchema map[string]any
	if args.ExpectedAttributesSchemaFor != nil && nodeDef.Executor != "" {
		if raw, ok := args.ExpectedAttributesSchemaFor(nodeDef.Executor); ok && len(raw) > 0 {
			if err := json.Unmarshal(raw, &execSchema); err != nil {
				if args.Logger != nil {
					args.Logger.Warn("firstRunAttributeBaseline: executor schema unmarshal failed",
						"executor", nodeDef.Executor, "error", err.Error())
				}
				execSchema = nil
			}
		}
	}
	l1Defaults := templateAttributeDefaultsFor(tmpl, nodeDef.Executor)
	if execSchema == nil && nodeSchema == nil {
		return map[string]any{}, nil
	}
	effectiveSchema := node.MergeAttributeDefaults(execSchema, l1Defaults, nodeSchema)
	return mergeSchemaDefaultsForDispatch(effectiveSchema, map[string]any{}), nil
}

func diffAttributesData(prior, current map[string]any) map[string]any {
	out := map[string]any{}
	seen := map[string]bool{}
	for k, v := range current {
		seen[k] = true
		pv, ok := prior[k]
		if !ok || !attributesValueEqual(pv, v) {
			out[k] = v
		}
	}
	for k := range prior {
		if !seen[k] {
			out[k] = nil
		}
	}
	return out
}

func attributesValueEqual(a, b any) bool {
	aj, errA := json.Marshal(a)
	bj, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

func upsertDataFromDispatchInputBagIfEmpty(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	runID, nodeID foundationshared.UUID,
) error {
	current, err := args.Persist.NodeAttributes().GetByRun(ctx, runID, tx)
	if err != nil {
		return err
	}
	if current != nil && len(current.Data) > 0 {
		return nil
	}
	bag, err := args.Persist.NodeAttributes().GetDispatchInputBag(ctx, tx, runID)
	if err != nil {
		return err
	}
	if bag == nil {
		bag = map[string]any{}
	}
	return args.Persist.NodeAttributes().Upsert(ctx, runID, nodeID, bag, tx)
}
