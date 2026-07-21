// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"encoding/json"
	"fmt"
	"strings"

	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
)

func isValidFallbackLiteral(s string) bool {
	if s == "null" || s == "true" || s == "false" {
		return true
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if err := json.Unmarshal([]byte(s), &str); err == nil {
			return true
		}
		return false
	}
	var n float64
	return json.Unmarshal([]byte(s), &n) == nil
}

// @concept: attribute
func checkAttributeSource(src, path string, declared map[string]int, directAliases, heldAliases map[string]struct{}, spec *TemplateSpec, res *ValidationResult) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: path, Msg: "source is empty",
		})
		return
	}
	matches := dispatchDirectiveRe.FindAllStringSubmatchIndex(trimmed, -1)
	if len(matches) == 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source must contain at least one {{...}} directive, got %q", trimmed),
		})
		return
	}
	for _, m := range matches {
		body := strings.TrimSpace(trimmed[m[2]:m[3]])
		checkAttributeDirectiveBody(body, path, declared, directAliases, heldAliases, spec, res)
	}
}

func rejectEmptySegments(parts []string, from int, kindLabel, body, path string, res *ValidationResult) bool {
	if from >= len(parts) {
		return false
	}
	for _, p := range parts[from:] {
		if p == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("%s directive %q has an empty trailing segment", kindLabel, body),
			})
			return true
		}
	}
	return false
}

func paramsTopKey(rest string) string {
	if dot := strings.Index(rest, "."); dot >= 0 {
		return rest[:dot]
	}
	return rest
}

func checkParamsKeyDeclared(rest, path, body string, spec *TemplateSpec, res *ValidationResult) {
	if spec == nil || spec.ParamsSchema == nil {
		return
	}
	topKey := paramsTopKey(rest)
	props := paramsSchemaProperties(spec)
	if _, ok := props[topKey]; ok {
		return
	}
	res.Errors = append(res.Errors, ValidationError{
		Path: path,
		Msg: fmt.Sprintf("directive %q references undeclared params key %q (declare it under params_schema.properties)",
			body, topKey),
	})
}

func checkAttributeDirectiveBody(rawBody, path string, declared map[string]int, directAliases, heldAliases map[string]struct{}, spec *TemplateSpec, res *ValidationResult) {
	rawBody = strings.TrimSpace(rawBody)
	shape := attributes.ParseDirectiveShape(rawBody)
	if shape.MultiPipe {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source directive %q has a multi-pipe fallback chain (only one literal admitted)", rawBody),
		})
		return
	}
	if shape.HasFallback && !isValidFallbackLiteral(shape.FallbackLiteral) {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source directive %q fallback literal %q must be null, true, false, a JSON number, or a quoted string", rawBody, shape.FallbackLiteral),
		})
		return
	}
	if shape.Lenient && shape.HasFallback {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  "source directive has both `?` marker and `| <literal>` fallback — pick one (incoherent: `?` says null on missing, `|` says literal on missing)",
		})
		return
	}
	body := shape.Body
	bodyMatch := directiveBodyRe.FindStringSubmatch(body)
	if bodyMatch == nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source directive %q must start with claim.|params.|nodes.|messages.|child.|env.", body),
		})
		return
	}
	kind := bodyMatch[1]
	rest := bodyMatch[2]
	parts := strings.Split(rest, ".")
	switch kind {
	case "claim":
		if len(parts) < 2 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q must be claim.<alias>.{address|claim_scope|payload[.<field>]}", body),
			})
			return
		}
		alias := parts[0]
		switch parts[1] {
		case "address", "claim_scope":
			if len(parts) != 2 {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("claim.<alias>.%s takes no further field path", parts[1]),
				})
			}
		case "payload":
			rejectEmptySegments(parts, 2, "claim", body, path, res)
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q second segment must be address|claim_scope|payload", body),
			})
		}
		_, isOwn := directAliases[alias]
		_, isHeld := heldAliases[alias]
		if !isOwn && !isHeld {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive references alias %q which is neither acquired here (claim_producers:) nor declared in holds:", alias),
			})
		}
	case "params":
		if len(parts) < 1 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("params directive %q must be params.<key>", body),
			})
			return
		}
		if rejectEmptySegments(parts, 1, "params", body, path, res) {
			return
		}
		if !shape.HasFallback && !shape.Lenient {
			checkParamsKeyDeclared(rest, path, body, spec, res)
		}
	case "nodes":
		if len(parts) < 2 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("nodes directive %q must be nodes.<node>.attribute[.<...>]", body),
			})
			return
		}
		switch parts[1] {
		case "attribute":
			if rejectEmptySegments(parts, 2, "nodes", body, path, res) {
				return
			}
			if _, ok := declared[parts[0]]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("nodes directive references unknown node %q", parts[0]),
				})
			}
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("nodes directive %q second segment must be 'attribute'", body),
			})
		}
	case "child":
		if len(parts) != 1 || parts[0] != "partition_key" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("child directive %q must be child.partition_key", body),
			})
		}
	case "messages":
		if len(parts) < 1 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("messages directive %q must be messages.<type>[.<field>]", body),
			})
			return
		}
		rejectEmptySegments(parts, 1, "messages", body, path, res)
	case "env":
		if len(parts) != 1 || !envVarNameRe.MatchString(parts[0]) {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("env directive %q must be env.<VAR_NAME> where the name matches [A-Za-z_][A-Za-z0-9_]*", body),
			})
		}
	}
}

func checkScopeDirectives(s, path string, spec *TemplateSpec, res *ValidationResult) {
	checkDispatchDirectives(s, path, spec, res)
	stripped := dispatchDirectiveRe.ReplaceAllString(s, "")
	for _, m := range anyBraceRe.FindAllString(stripped, -1) {
		if !instantiationPlaceholderRe.MatchString(m) {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("invalid placeholder %q (expected {params.<key>} or {{...}} directive)", m),
			})
		}
	}
}

func checkLockNameDirectives(s, path string, spec *TemplateSpec, res *ValidationResult) {
	checkScopeDirectives(s, path, spec, res)
}

func checkDispatchDirectives(s, path string, spec *TemplateSpec, res *ValidationResult) {
	for _, m := range dispatchDirectiveRe.FindAllStringSubmatch(s, -1) {
		body := strings.TrimSpace(m[1])
		if body == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  "empty {{...}} directive",
			})
			continue
		}
		bodyMatch := directiveBodyRe.FindStringSubmatch(body)
		if bodyMatch == nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("invalid directive %q (expected claim.<a>.{address|claim_scope|payload[.<f>]}, params.<k>, nodes.<n>.attribute[.<f>], messages.<type>[.<field>], child.partition_key, or env.<VAR_NAME>)", body),
			})
			continue
		}
		if bodyMatch[1] == "params" {
			checkParamsKeyDeclared(bodyMatch[2], path, body, spec, res)
		}
	}
}
