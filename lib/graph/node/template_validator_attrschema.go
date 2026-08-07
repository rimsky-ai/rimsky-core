// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// @concept: template
func validateParamsSchema(spec *TemplateSpec, res *ValidationResult) {
	if spec == nil || spec.ParamsSchema == nil {
		return
	}
	schemaBytes, err := json.Marshal(spec.ParamsSchema)
	if err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: "params_schema", Msg: fmt.Sprintf("failed to marshal params_schema for parse: %v", err),
		})
		return
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("params-schema.json", bytes.NewReader(schemaBytes)); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: "params_schema", Msg: fmt.Sprintf("params_schema is not valid JSON Schema: %v", err),
		})
		return
	}
	if _, err := compiler.Compile("params-schema.json"); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: "params_schema", Msg: fmt.Sprintf("params_schema does not compile: %v", err),
		})
	}
}

// @concept: attribute
func validateAttributesSchema(n TemplateNodeDef, base string, declared map[string]int, spec *TemplateSpec, hooks RegistryHooks, res *ValidationResult) {
	if n.Attributes == nil || len(n.Attributes.Schema) == 0 {
		return
	}
	sbase := fmt.Sprintf("%s.attributes.schema", base)

	schemaBytes, err := json.Marshal(n.Attributes.Schema)
	if err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase, Msg: fmt.Sprintf("failed to marshal schema for parse: %v", err),
		})
		return
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("template-attrs.json", bytes.NewReader(schemaBytes)); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase, Msg: fmt.Sprintf("schema is not valid JSON: %v", err),
		})
		return
	}
	if _, err := compiler.Compile("template-attrs.json"); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase, Msg: fmt.Sprintf("schema does not compile: %v", err),
		})
	}

	directAliases := make(map[string]struct{}, len(n.ClaimProducers))
	for _, s := range n.ClaimProducers {
		directAliases[s.AliasOf()] = struct{}{}
	}
	heldAliases := make(map[string]struct{}, len(n.Holds))
	for alias, binding := range n.Holds {
		heldAliases[EffectiveHoldsLocalAlias(alias, binding)] = struct{}{}
	}

	walkSchemaForSourcesWithPath(n.Attributes.Schema, "", func(raw any, path string) {
		src, ok := raw.(string)
		if !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.%s", sbase, path),
				Msg:  "source must be a string (array-form multi-source is not admitted)",
			})
			return
		}
		checkAttributeSource(src, fmt.Sprintf("%s.%s", sbase, path), declared, directAliases, heldAliases, spec, res)
	})

	executorForSchema := effectiveExecutor(n, hooks)

	var execSchema map[string]any
	var execReadOnlyProps map[string]bool
	execSchemaVisible := false
	if executorForSchema != "" && hooks.ExecutorExpectedAttributesSchema != nil {
		if execBytes, ok := hooks.ExecutorExpectedAttributesSchema(executorForSchema); ok {
			execSchemaVisible = true
			if len(execBytes) > 0 {
				if err := json.Unmarshal(execBytes, &execSchema); err != nil {
					res.Errors = append(res.Errors, ValidationError{
						Path: base + ".attributes",
						Msg:  fmt.Sprintf("executor %q expected_attributes_schema is not valid JSON: %v", executorForSchema, err),
					})
					return
				}
				execReadOnlyProps = extractReadOnlyProps(execSchema)
			}
		}
	}
	if execReadOnlyProps == nil {
		execReadOnlyProps = map[string]bool{}
	}

	if !execSchemaVisible && executorForSchema != "" && hooks.ExecutorExpectedAttributesSchema != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".attributes",
			Msg:  fmt.Sprintf("executor %q expected_attributes_schema is not visible at registration", executorForSchema),
		})
	}

	var l1Defaults map[string]any
	if spec != nil && spec.Defaults != nil && spec.Defaults.Attributes != nil && executorForSchema != "" {
		l1Defaults = spec.Defaults.Attributes.ByExecutor[executorForSchema]
	}

	effective := MergeAttributeDefaults(execSchema, l1Defaults, n.Attributes.Schema)
	checkAttributesSchema(effective, n.Attributes.Schema, execSchema, execReadOnlyProps, execSchemaVisible, sbase, res)
	if execSchemaVisible {
		validateCompositionAgainstExecutor(execSchema, l1Defaults, n.Attributes.Schema, sbase, res)
	}
}

// @concept: attribute
func validateCompositionAgainstExecutor(execSchema, l1Defaults, nodeSchema map[string]any, sbase string, res *ValidationResult) {
	if execSchema == nil {
		return
	}
	execProps, _ := execSchema["properties"].(map[string]any)
	nodeProps, _ := nodeSchema["properties"].(map[string]any)
	additionalProperties, hasAddProps := execSchema["additionalProperties"]
	closed := false
	if hasAddProps {
		if b, ok := additionalProperties.(bool); ok && !b {
			closed = true
		}
	}

	for name, raw := range nodeProps {
		nodeProp, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		nodeType, nodeHasType := nodeProp["type"]
		if !nodeHasType {
			continue
		}
		execProp, _ := execProps[name].(map[string]any)
		if execProp == nil {
			continue
		}
		execType, execHasType := execProp["type"]
		if !execHasType {
			continue
		}
		if !jsonValuesEqual(nodeType, execType) {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.properties.%s.type", sbase, name),
				Msg: fmt.Sprintf(
					"template declares property %s.type: %v but executor's expected_attributes_schema declares type: %v — the executor is authoritative on types",
					name, nodeType, execType),
			})
		}
	}

	if closed && execProps != nil {
		for name := range nodeProps {
			if _, declared := execProps[name]; !declared {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("%s.properties.%s", sbase, name),
					Msg: fmt.Sprintf(
						"property %s is not declared in executor's expected_attributes_schema and the executor's schema is closed (additionalProperties: false)",
						name),
				})
			}
		}
		for name := range l1Defaults {
			if _, declared := execProps[name]; declared {
				continue
			}
			if _, declaredInNode := nodeProps[name]; declaredInNode {
				continue
			}
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("defaults.attributes.by_executor.%s", name),
				Msg: fmt.Sprintf(
					"property %s is not declared in executor's expected_attributes_schema and the executor's schema is closed (additionalProperties: false)",
					name),
			})
		}
	}

	defaultsBag := map[string]any{}
	for name, val := range l1Defaults {
		defaultsBag[name] = val
	}
	for name, raw := range nodeProps {
		nodeProp, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if defaultVal, hasDefault := nodeProp["default"]; hasDefault {
			defaultsBag[name] = defaultVal
		}
	}
	if len(defaultsBag) == 0 {
		return
	}
	schemaForDefaults := SchemaWithoutTopLevelRequired(execSchema)
	if err := validateAgainstSchema(schemaForDefaults, defaultsBag); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.defaults", sbase),
			Msg: fmt.Sprintf(
				"composed default values violate executor's expected_attributes_schema: %v",
				err),
		})
	}
}

func SchemaWithoutTopLevelRequired(schema map[string]any) map[string]any {
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

func validateAgainstSchema(schema, data map[string]any) error {
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("composition.json", bytes.NewReader(schemaBytes)); err != nil {
		return fmt.Errorf("add resource: %w", err)
	}
	compiled, err := compiler.Compile("composition.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(dataBytes, &normalized); err != nil {
		return fmt.Errorf("unmarshal data: %w", err)
	}
	return compiled.Validate(normalized)
}

func jsonValuesEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

type AttributesSchemaCheckError struct {
	Path string
	Msg  string
}

// @concept: attribute
func CheckEffectiveAttributesSchema(effective, nodeSchema, execSchema map[string]any, executorReadOnlyProps map[string]bool, execSchemaVisible bool) []AttributesSchemaCheckError {
	var out []AttributesSchemaCheckError
	if effective == nil {
		return nil
	}
	effProps, _ := effective["properties"].(map[string]any)
	nodeProps, _ := nodeSchema["properties"].(map[string]any)
	execProps, _ := execSchema["properties"].(map[string]any)
	schemaHasNoProps := IsPermissiveExecutorSchema(execSchema)
	execOpen := executorSchemaAllowsExtensions(execSchema)
	for name, raw := range effProps {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		_, hasSource := prop["source"]
		_, hasDefault := prop["default"]
		execRO := executorReadOnlyProps[name]
		if hasSource && hasDefault {
			out = append(out, AttributesSchemaCheckError{
				Path: fmt.Sprintf("properties.%s", name),
				Msg:  "property declares both `source:` and `default:` — pick one",
			})
			continue
		}
		_, execEnumerates := execProps[name]
		propUnconstrained := schemaHasNoProps || (execOpen && !execEnumerates)
		if !hasSource && !hasDefault && !execRO && execSchemaVisible && !propUnconstrained {
			out = append(out, AttributesSchemaCheckError{
				Path: fmt.Sprintf("properties.%s", name),
				Msg:  "property has no `source:`, no `default:`, and is not marked `readOnly: true` in the executor's expected_attributes_schema — declare one of these or the property is unpopulated at dispatch",
			})
			continue
		}
		if nodeProps != nil && execSchemaVisible && !propUnconstrained {
			if rawNode, present := nodeProps[name]; present {
				if nodeProp, ok := rawNode.(map[string]any); ok {
					if ro, _ := nodeProp["readOnly"].(bool); ro && !execRO {
						out = append(out, AttributesSchemaCheckError{
							Path: fmt.Sprintf("properties.%s", name),
							Msg:  "template marks property `readOnly: true` but the executor's expected_attributes_schema does not — the executor is authoritative on which properties it produces",
						})
					}
				}
			}
		}
	}
	return out
}

// @concept: attribute
func IsPermissiveExecutorSchema(execSchema map[string]any) bool {
	if execSchema == nil {
		return false
	}
	_, hasProps := execSchema["properties"]
	return !hasProps
}

// @concept: attribute
func executorSchemaAllowsExtensions(execSchema map[string]any) bool {
	if execSchema == nil {
		return false
	}
	add, has := execSchema["additionalProperties"]
	if !has {
		return false
	}
	if b, ok := add.(bool); ok {
		return b
	}
	return true
}

// @concept: attribute
func checkAttributesSchema(effective, nodeSchema, execSchema map[string]any, executorReadOnlyProps map[string]bool, execSchemaVisible bool, sbase string, res *ValidationResult) {
	for _, e := range CheckEffectiveAttributesSchema(effective, nodeSchema, execSchema, executorReadOnlyProps, execSchemaVisible) {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase + "." + e.Path,
			Msg:  e.Msg,
		})
	}
}

// @concept: attribute
func extractReadOnlyProps(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	if schema == nil {
		return out
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ro, _ := prop["readOnly"].(bool); ro {
			out[name] = true
		}
	}
	return out
}

// @concept: attribute
func MergeAttributeDefaults(execSchema map[string]any, l1Defaults map[string]any, nodeSchema map[string]any) map[string]any {
	out := deepCopyJSON(execSchema)
	if out == nil {
		out = map[string]any{}
	}
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		out["properties"] = props
	}
	for attr, val := range l1Defaults {
		prop, _ := props[attr].(map[string]any)
		if prop == nil {
			prop = map[string]any{}
			props[attr] = prop
		}
		prop["default"] = val
	}
	if nodeSchema != nil {
		if nodeProps, ok := nodeSchema["properties"].(map[string]any); ok {
			for attr, raw := range nodeProps {
				nodeProp, _ := raw.(map[string]any)
				if nodeProp == nil {
					continue
				}
				existing, _ := props[attr].(map[string]any)
				if existing == nil {
					props[attr] = deepCopyJSON(nodeProp)
					continue
				}
				if _, l2HasSource := nodeProp["source"]; l2HasSource {
					delete(existing, "default")
				}
				if _, l2HasDefault := nodeProp["default"]; l2HasDefault {
					delete(existing, "source")
				}
				for k, v := range nodeProp {
					existing[k] = v
				}
			}
		}
		if nodeReq, ok := nodeSchema["required"].([]any); ok && len(nodeReq) > 0 {
			existingReq, _ := out["required"].([]any)
			seen := map[string]bool{}
			for _, r := range existingReq {
				if s, ok := r.(string); ok {
					seen[s] = true
				}
			}
			for _, r := range nodeReq {
				if s, ok := r.(string); ok && !seen[s] {
					existingReq = append(existingReq, r)
					seen[s] = true
				}
			}
			out["required"] = existingReq
		}
		if _, execHas := out["additionalProperties"]; !execHas {
			if v, ok := nodeSchema["additionalProperties"]; ok {
				out["additionalProperties"] = v
			}
		}
	}
	return out
}

func deepCopyJSON(v map[string]any) map[string]any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
