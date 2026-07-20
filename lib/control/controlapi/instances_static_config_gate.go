// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance
package controlapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

var errStaticConfigViolation = errors.New("instantiation static-config validation failed")

type staticConfigGateError struct {
	NodeType string
	Executor string
	Cause    error
}

func (e *staticConfigGateError) Error() string {
	return fmt.Sprintf(
		"node %q (executor %q) static attribute config violates the executor's expected_attributes_schema: %v",
		e.NodeType, e.Executor, e.Cause)
}

func (e *staticConfigGateError) Unwrap() error { return e.Cause }

func (e *staticConfigGateError) Is(target error) bool {
	return target == errStaticConfigViolation || target == foundationshared.ErrTemplateValidation
}

func (e *staticConfigGateError) validationErrorEntry() map[string]string {
	return map[string]string{
		"path": fmt.Sprintf("nodes[%s].attributes", e.NodeType),
		"msg":  e.Cause.Error(),
	}
}

func validateStaticConfigAgainstExecutorSchemas(
	nodes []nodepkg.TemplateNodeDef,
	defaults *nodepkg.TemplateDefaults,
	execCapabilities func(string) (declaredEvents []string, declaredErrorClasses []string, expectedAttributesSchema []byte, ok bool),
) error {
	if execCapabilities == nil {
		return nil
	}
	for _, n := range nodes {
		if n.Executor == "" {
			continue
		}
		execSchema, ok := lookupExecutorSchema(n.Executor, execCapabilities)
		if !ok {
			continue
		}
		bag := composeStaticConfigBag(n, defaults)
		if len(bag) == 0 {
			continue
		}
		schemaForStatic := schemaWithoutTopLevelRequiredLocal(execSchema)
		if err := attributes.Validate(schemaForStatic, bag, attributes.PhaseDispatch); err != nil {
			return &staticConfigGateError{
				NodeType: n.Type,
				Executor: n.Executor,
				Cause:    err,
			}
		}
	}
	return nil
}

func lookupExecutorSchema(
	executor string,
	execCapabilities func(string) ([]string, []string, []byte, bool),
) (map[string]any, bool) {
	_, _, schemaBytes, ok := execCapabilities(executor)
	if !ok || len(schemaBytes) == 0 {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		slog.Default().Warn("instances_static_config_gate.malformed_executor_schema",
			"executor", executor, "error", err.Error())
		return nil, false
	}
	return schema, true
}

func composeStaticConfigBag(n nodepkg.TemplateNodeDef, defaults *nodepkg.TemplateDefaults) map[string]any {
	bag := map[string]any{}
	if defaults != nil && defaults.Attributes != nil {
		for name, val := range defaults.Attributes.ByExecutor[n.Executor] {
			bag[name] = val
		}
	}
	if n.Attributes == nil {
		return bag
	}
	nodeProps, _ := n.Attributes.Schema["properties"].(map[string]any)
	for name, raw := range nodeProps {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasSource := prop["source"]; hasSource {
			delete(bag, name)
			continue
		}
		if defaultVal, hasDefault := prop["default"]; hasDefault {
			bag[name] = defaultVal
		}
	}
	return bag
}

func schemaWithoutTopLevelRequiredLocal(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	if _, hasRequired := schema["required"]; !hasRequired {
		return schema
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if k == "required" {
			continue
		}
		out[k] = v
	}
	return out
}
