// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: attribute
func applyAttributeOverrides(
	resolved map[string]any,
	overrides map[string]any,
	executor string,
	nodeName string,
	graph string,
	childKey string,
	logger shared.Logger,
) (merged map[string]any, matched []int) {
	mergedAny := shared.DeepMergeJSON(resolved, nil)
	if len(overrides) == 0 {
		if m, ok := mergedAny.(map[string]any); ok {
			return m, nil
		}
		return map[string]any{}, nil
	}

	if frag, ok := lookupFragment(overrides, "by_executor", executor, logger); ok {
		mergedAny = shared.DeepMergeJSON(mergedAny, frag)
	}
	if frag, ok := lookupFragment(overrides, "by_node", nodeName, logger); ok {
		mergedAny = shared.DeepMergeJSON(mergedAny, frag)
	}

	matcherCtx, _ := mergedAny.(map[string]any)
	if matcherCtx == nil {
		matcherCtx = map[string]any{}
	}

	if entries, ok := lookupMatchList(overrides, logger); ok {
		for i, entry := range entries {
			if entry == nil {
				continue
			}
			matcherMap, _ := entry["matcher"].(map[string]any)
			overlay, _ := entry["overlay"].(map[string]any)
			if !evaluateMatcher(matcherMap, executor, nodeName, graph, childKey, matcherCtx, logger, i) {
				continue
			}
			if overlay != nil {
				mergedAny = shared.DeepMergeJSON(mergedAny, overlay)
			}
			matched = append(matched, i)
		}
	}

	if m, ok := mergedAny.(map[string]any); ok {
		return m, matched
	}
	if logger != nil {
		logger.Warn("applyAttributeOverrides: merge produced non-map root; falling back to resolved",
			"executor", executor,
			"node_name", nodeName)
	}
	cloned, _ := shared.DeepMergeJSON(resolved, nil).(map[string]any)
	return cloned, matched
}

func lookupFragment(overrides map[string]any, key, subkey string, logger shared.Logger) (map[string]any, bool) {
	raw, ok := overrides[key]
	if !ok {
		return nil, false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		if logger != nil {
			logger.Warn("applyAttributeOverrides: override key has malformed shape; skipping",
				"key", key)
		}
		return nil, false
	}
	frag, ok := m[subkey]
	if !ok {
		return nil, false
	}
	fm, ok := frag.(map[string]any)
	if !ok {
		if logger != nil {
			logger.Warn("applyAttributeOverrides: override fragment has malformed shape; skipping",
				"key", key, "subkey", subkey)
		}
		return nil, false
	}
	return fm, true
}

func lookupMatchList(overrides map[string]any, logger shared.Logger) ([]map[string]any, bool) {
	raw, ok := overrides["by_match"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			if logger != nil {
				logger.Warn("applyAttributeOverrides: by_match entry has malformed shape; skipping",
					"entry_index", i)
			}
			continue
		}
		out[i] = m
	}
	return out, true
}

func evaluateMatcher(
	m map[string]any,
	executor, nodeName, graph, childKey string,
	bag map[string]any,
	logger shared.Logger,
	entryIndex int,
) bool {
	return matcher.Evaluate(matcher.Matcher(m), matcher.Context{
		Executor:     executor,
		NodeType:     nodeName,
		Graph:        graph,
		ChildKey:     childKey,
		AttributeBag: bag,
	}, logger, entryIndex)
}

type matchEventPersist interface {
	Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error
	Events() persistence.EventTable
}

// @concept: attribute
func emitOverrideMatchEventsAfterMerge(
	ctx context.Context,
	persist matchEventPersist,
	logger shared.Logger,
	instanceID shared.UUID,
	matched []int,
) error {
	if len(matched) == 0 {
		return nil
	}
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, idx := range matched {
			if err := persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &instanceID,
				Kind:       events.KindAttributeOverrideMatched(),
				Payload:    map[string]any{"override_index": idx},
			}, tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if logger != nil {
			logger.Warn("instance.attribute_override_match_event_failed",
				"instance_id", instanceID.String(),
				"matched_indices", matched,
				"error", err.Error())
		}
		return fmt.Errorf("emit attribute-override match events: %w", err)
	}
	return nil
}
