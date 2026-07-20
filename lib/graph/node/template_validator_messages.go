// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// @concept: message-sender-node
func validateSendsMessage(n TemplateNodeDef, base string, spec *TemplateSpec, declaredMessages map[string]struct{}, res *ValidationResult) {
	if n.SendsMessage == "" {
		return
	}
	mt := strings.TrimSpace(n.SendsMessage)
	if mt == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".sends_message",
			Msg:  "sends_message must not be whitespace-only",
		})
		return
	}
	if _, ok := declaredMessages[mt]; !ok {
		declaredList := make([]string, 0, len(declaredMessages))
		for k := range declaredMessages {
			declaredList = append(declaredList, k)
		}
		sort.Strings(declaredList)
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".sends_message",
			Msg: fmt.Sprintf(
				"sends_message references unknown message type %q (declared types: %v)",
				mt, declaredList),
		})
		return
	}
	var dest *MessageSchema
	if spec != nil {
		for i := range spec.Messages {
			if strings.TrimSpace(spec.Messages[i].Type) == mt {
				dest = &spec.Messages[i]
				break
			}
		}
	}
	if dest == nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".sends_message",
			Msg: fmt.Sprintf(
				"sends_message %q is in the declared set but not resolvable in messages: registry (internal validator drift)",
				mt),
		})
		return
	}
	var nodeSchema map[string]any
	if n.Attributes != nil {
		nodeSchema = n.Attributes.Schema
	}
	// @concept: message-sender-node
	var bodyShape map[string]any
	if len(dest.BodySchema) > 0 {
		var raw any
		if err := json.Unmarshal(dest.BodySchema, &raw); err == nil {
			if m, ok := raw.(map[string]any); ok {
				bodyShape = m
			}
		}
	}
	bodyProps := sendsMessageProperties(bodyShape)
	nodeProps := sendsMessageProperties(nodeSchema)
	bodyRequired := sendsMessageRequiredSet(bodyShape)
	nodeRequired := sendsMessageRequiredSet(nodeSchema)

	for name := range nodeProps {
		if _, ok := bodyProps[name]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.properties.%s", base, name),
				Msg: fmt.Sprintf(
					"send-node attribute %q is not declared in destination message type %q's body_schema (the attribute set must match the body shape exactly)",
					name, mt),
			})
		}
	}
	for name := range bodyProps {
		if _, ok := nodeProps[name]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.properties", base),
				Msg: fmt.Sprintf(
					"send-node attributes schema is missing field %q declared in destination message type %q's body_schema (the attribute set must match the body shape exactly)",
					name, mt),
			})
		}
	}
	for name, np := range nodeProps {
		bp, ok := bodyProps[name]
		if !ok {
			continue
		}
		npType, npHasType := np["type"]
		bpType, bpHasType := bp["type"]
		if npHasType && bpHasType && !jsonValuesEqual(npType, bpType) {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.properties.%s.type", base, name),
				Msg: fmt.Sprintf(
					"send-node attribute %q declares type %v but destination message type %q's body_schema declares type %v (types must match exactly)",
					name, npType, mt, bpType),
			})
		}
	}
	for r := range nodeRequired {
		if _, ok := bodyRequired[r]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.required", base),
				Msg: fmt.Sprintf(
					"send-node requires %q but destination message type %q's body_schema does not require it (required: sets must match exactly)",
					r, mt),
			})
		}
	}
	for r := range bodyRequired {
		if _, ok := nodeRequired[r]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.required", base),
				Msg: fmt.Sprintf(
					"destination message type %q's body_schema requires %q but send-node attributes schema does not require it (required: sets must match exactly)",
					mt, r),
			})
		}
	}
}

func sendsMessageProperties(schema map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	if schema == nil {
		return out
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return out
	}
	for name, raw := range props {
		if m, ok := raw.(map[string]any); ok {
			out[name] = m
		} else {
			out[name] = map[string]any{}
		}
	}
	return out
}

func sendsMessageRequiredSet(schema map[string]any) map[string]struct{} {
	out := map[string]struct{}{}
	if schema == nil {
		return out
	}
	raw, ok := schema["required"]
	if !ok {
		return out
	}
	list, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, v := range list {
		if s, ok := v.(string); ok {
			out[s] = struct{}{}
		}
	}
	return out
}

// @concept: message-schema
func validatePublishers(spec *TemplateSpec, declaredMessages map[string]struct{}, res *ValidationResult) {
	seenNames := make(map[string]struct{}, len(spec.Publishers))
	for i, p := range spec.Publishers {
		base := fmt.Sprintf("publishers[%d]", i)
		if strings.TrimSpace(p.Name) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".name", Msg: "name is required",
			})
		} else if _, dup := seenNames[p.Name]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".name",
				Msg:  fmt.Sprintf("duplicate publisher name %q", p.Name),
			})
		} else {
			seenNames[p.Name] = struct{}{}
		}
		if strings.TrimSpace(p.Kind) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".kind", Msg: "kind is required",
			})
		}
		mt := strings.TrimSpace(p.MessageType)
		if mt == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".message_type",
				Msg:  "message_type is required (cannot be empty)",
			})
			continue
		}
		if _, ok := declaredMessages[mt]; !ok {
			declaredList := make([]string, 0, len(declaredMessages))
			for k := range declaredMessages {
				declaredList = append(declaredList, k)
			}
			sort.Strings(declaredList)
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".message_type",
				Msg: fmt.Sprintf(
					"message_type %q is not declared in the template's `messages:` registry (declared types: %v)",
					mt, declaredList),
			})
		}
	}
}

// @concept: message-schema
func buildMessageBodyFieldSet(spec *TemplateSpec) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for _, m := range spec.Messages {
		t := strings.TrimSpace(m.Type)
		if len(m.BodySchema) == 0 {
			continue
		}
		var shape map[string]any
		if err := json.Unmarshal(m.BodySchema, &shape); err != nil {
			continue
		}
		props, ok := shape["properties"].(map[string]any)
		if !ok {
			out[t] = map[string]struct{}{}
			continue
		}
		fields := make(map[string]struct{}, len(props))
		for k := range props {
			fields[k] = struct{}{}
		}
		out[t] = fields
	}
	return out
}

// @concept: message-schema
func validateMessages(spec *TemplateSpec, declared map[string]int, res *ValidationResult) map[string]struct{} {
	declaredMessages := make(map[string]struct{}, len(spec.Messages))
	if len(spec.Messages) == 0 {
		return declaredMessages
	}
	for i, m := range spec.Messages {
		base := fmt.Sprintf("messages[%d]", i)
		t := strings.TrimSpace(m.Type)
		if t == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  `type "" is reserved-for-runtime (the implicit empty-message wake trigger seeded automatically at registration; author-declared empty-type entries are refused)`,
			})
			continue
		}
		if strings.ContainsAny(t, " \t\n\r") {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("type %q must not contain whitespace", t),
			})
			continue
		}
		if strings.HasPrefix(t, "/") || strings.HasSuffix(t, "/") {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("type %q must not start or end with `/`", t),
			})
			continue
		}
		segmentsValid := true
		segmentHasDot := false
		for _, seg := range strings.Split(t, "/") {
			if seg == "" {
				segmentsValid = false
				continue
			}
			if strings.Contains(seg, ".") {
				segmentHasDot = true
			}
		}
		segmentErrored := false
		if !segmentsValid {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("type %q has empty segment(s)", t),
			})
			segmentErrored = true
		}
		if segmentHasDot {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg: fmt.Sprintf(
					"type %q segments must not contain `.` (the substitution-directive parser splits on `.`)",
					t),
			})
			segmentErrored = true
		}
		if segmentErrored {
			continue
		}
		if !strings.Contains(t, "/") {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg: fmt.Sprintf(
					"type %q must be a slash-bearing type-path (e.g. `category/action`) so it cannot collide with a node-type",
					t),
			})
			continue
		}
		if _, dup := declaredMessages[t]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("duplicate message type %q", t),
			})
			continue
		}
		if _, nodeCollision := declared[t]; nodeCollision {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg: fmt.Sprintf(
					"message type %q collides with a declared node type of the same name; pick a distinct type-path so subscriptions can disambiguate",
					t),
			})
			continue
		}
		declaredMessages[t] = struct{}{}
		if len(m.BodySchema) == 0 {
			continue
		}
		var schemaShape any
		if err := json.Unmarshal(m.BodySchema, &schemaShape); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  fmt.Sprintf("body_schema is not valid JSON: %v", err),
			})
			continue
		}
		if _, ok := schemaShape.(map[string]any); !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  "body_schema must be a JSON Schema object",
			})
			continue
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("body-schema.json", bytes.NewReader(m.BodySchema)); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  fmt.Sprintf("body_schema is not valid JSON Schema: %v", err),
			})
			continue
		}
		if _, err := compiler.Compile("body-schema.json"); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  fmt.Sprintf("body_schema does not compile: %v", err),
			})
			continue
		}
	}
	return declaredMessages
}

// @concept: terminal-tag
var payloadTagsLiteralRE = regexp.MustCompile(`["']([^"']+)["']\s+in\s+payload\.tags|payload\.tags\.contains\(\s*["']([^"']+)["']\s*\)`)

// @concept: terminal-tag
func extractPayloadTagLiterals(when string) []string {
	if when == "" {
		return nil
	}
	var tags []string
	seen := map[string]struct{}{}
	for _, match := range payloadTagsLiteralRE.FindAllStringSubmatch(when, -1) {
		for _, g := range match[1:] {
			if g == "" {
				continue
			}
			if _, dup := seen[g]; dup {
				continue
			}
			seen[g] = struct{}{}
			tags = append(tags, g)
		}
	}
	return tags
}
