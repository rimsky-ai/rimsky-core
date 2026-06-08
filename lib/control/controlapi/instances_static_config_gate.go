// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instances_static_config_gate.go — the mandatory instantiation-time
// static-config validation gate.
//
// Registration-time reference validation is operator-controlled
// (concept:template, story S-template-validation-ref-validation-mode):
// modes `available` / `none` may skip validating a node's static
// attribute config against the referenced executor's schema (e.g. the
// executor was not yet provisioned, or validation is off entirely).
// Whatever a relaxed mode skipped is NOT skipped forever — instantiation
// is the mandatory gate. By the time `POST /instances` runs, the
// template is deployed and all referenced services exist (the
// bound-on-demand host-agent proxy is itself a present service), so the
// statically-knowable config can — and must — be value-validated here.
//
// This gate validates ONLY the statically-knowable subset of each node's
// attribute bag: the composed L1 template defaults ∪ L2 node-declared
// `default:` literals. Substitution-sourced values (`source:`-bound and
// `{{...}}` directive values) are knowable only once a node acquires its
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

// errStaticConfigViolation is the sentinel the instantiation-time
// static-config gate returns. The instance-create handler routes it to
// HTTP 400 — distinct from the bare ErrTemplateValidation branch (which
// the handler maps to 409 for template state-machine conflicts). The
// returned error ALSO wraps ErrTemplateValidation so the failure is
// typed as a template-validation error for any callers/audit consumers
// that branch on that sentinel; the 400 status is chosen by the
// handler's earlier errStaticConfigViolation branch.
var errStaticConfigViolation = errors.New("instantiation static-config validation failed")

// staticConfigGateError carries the offending node + the underlying
// schema-validation cause (which names the attribute path and the
// violated constraint, e.g. `minimum`). It satisfies errors.Is for both
// errStaticConfigViolation (status routing) and ErrTemplateValidation
// (typed-error semantics), and exposes the per-node finding so the
// handler can render a structured `validation_errors` body.
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

// Is lets errors.Is(err, errStaticConfigViolation) and
// errors.Is(err, ErrTemplateValidation) both match, so the failure
// routes to 400 (via the gate-specific branch) yet remains typed as a
// template-validation error.
func (e *staticConfigGateError) Is(target error) bool {
	return target == errStaticConfigViolation || target == foundationshared.ErrTemplateValidation
}

// validationErrorEntry renders the finding for the HTTP `validation_errors`
// array. The path names the offending node + attribute surface; msg
// carries the schema-validation message (which cites the attribute path
// and the violated constraint).
func (e *staticConfigGateError) validationErrorEntry() map[string]string {
	return map[string]string{
		"path": fmt.Sprintf("nodes[%s].attributes", e.NodeType),
		"msg":  e.Cause.Error(),
	}
}

// validateStaticConfigAgainstExecutorSchemas is the mandatory
// instantiation-time static-config validation gate. For every node in
// the (canonicalized, flat) template that references an executor whose
// expected_attributes_schema is visible, it composes the node's
// statically-knowable attribute bag (L1 template defaults ∪ L2
// node-declared `default:` literals) and value-validates it against the
// executor's schema. The first violation found is returned as a
// *staticConfigGateError; nil means every node's static config is
// schema-clean.
//
// Source-bound and substitution-directive values are intentionally NOT
// in the composed bag — they are not statically knowable and stay
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
		// No way to look up any executor's schema — nothing to validate.
		// (The dispatch-time gate still enforces the schema when reached.)
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
		// Strip the executor schema's top-level `required` — the static
		// bag is a partial subset of the dispatch bag, so a `required`
		// entry bound via `source:` or written by the executor has no
		// value here and would false-positive. We only want the value
		// constraints (minimum, type, enum, …) on the values that ARE
		// present.
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

// lookupExecutorSchema resolves and JSON-decodes the executor's
// expected_attributes_schema via the capabilities lookup. Returns
// (schema, true) only when the lookup reports ok AND the schema is
// non-empty AND parses as a JSON object; otherwise (nil, false) so the
// caller skips the node (deferring to the dispatch-time gate).
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
		// A schema that does not parse is the executor's contract bug,
		// not the instantiating operator's. Skip rather than reject the
		// create on it — registration's own validator (under modes that
		// look at the schema) and the dispatch gate surface a parse
		// failure with the right diagnostic.
		return nil, false
	}
	return schema, true
}

// composeStaticConfigBag builds the statically-knowable attribute value
// bag for one node: L1 template defaults for the node's executor, then
// L2 node-declared `properties[*].default` literals (L2 wins on
// collision — the most-specific-wins rule). Values bound via `source:`
// or carrying a `{{...}}` substitution directive are NOT included —
// those are not statically knowable and stay dispatch-validated.
//
// @source: lib/graph/node/template_validator.go::validateCompositionAgainstExecutor
func composeStaticConfigBag(n nodepkg.TemplateNodeDef, defaults *nodepkg.TemplateDefaults) map[string]any {
	bag := map[string]any{}
	// L1: template-author defaults routed to this node's executor.
	if defaults != nil && defaults.Attributes != nil {
		for name, val := range defaults.Attributes.ByExecutor[n.Executor] {
			bag[name] = val
		}
	}
	// L2: per-node `default:` literals override L1. A property carrying a
	// `source:` directive is excluded entirely — only literal `default:`
	// values are static.
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
			// Source-bound: not statically knowable. Leave any L1 default
			// for the same property in place only if L1 itself supplied a
			// literal (it does not carry `source:`); but a `source:`-bound
			// L2 property supersedes L1's static contribution, so drop it.
			delete(bag, name)
			continue
		}
		if defaultVal, hasDefault := prop["default"]; hasDefault {
			bag[name] = defaultVal
		}
	}
	return bag
}

// schemaWithoutTopLevelRequiredLocal returns a shallow clone of schema
// with the top-level `required` key removed. The static-config bag is a
// proper subset of the dispatch-time bag, so top-level `required`
// enforcement against it fires false positives. The clone is shallow —
// nested schemas keep their `required:` keys.
//
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
