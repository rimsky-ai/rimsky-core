// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// inputs, so they stay validated at dispatch (@blessed-invariant 12 —
// attributes validate twice). For the static part, this gate is the
// early, create-time enforcement and the dispatch pass becomes
// defense-in-depth.
//
// @concept: instance
package controlapi

import (
	"encoding/json"
	"errors"
	"fmt"

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

// validated at dispatch (@blessed-invariant 12). The executor schema's
// top-level `required` is stripped before validating because the static
// bag is a proper subset of the dispatch bag (source-bound and
// executor-written properties are absent), so enforcing `required` here
// would fire false-positive missing-property errors. This mirrors the
// registration-time defaults-bag pass.
//
// Schema visibility: a node whose referenced executor's schema is not
// visible (lookup returns ok=false / empty) is skipped here — the
// dispatch-time executor_schema_unavailable gate is the loud backstop
// for a genuinely-missing schema. In practice instantiation runs after
// the template is deployed and the executor has handshaked, so the
// schema is visible for every referenced executor (the spec's
// "all referenced services exist at instantiation" precondition).
//
// @source: lib/graph/node/template_validator.go::validateCompositionAgainstExecutor
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
		return nil, false
	}
	return schema, true
}

// @source: lib/graph/node/template_validator.go::validateCompositionAgainstExecutor
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

// @source: lib/graph/node/template_validator.go::schemaWithoutTopLevelRequired
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
