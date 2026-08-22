// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attribute

import (
	"strings"
	"testing"
)

// @concept: inertness
// @concept: attribute
func TestValidate_ConstViolationNamesPathAndConstraintWithoutValue(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"token": map[string]any{"const": "expected-secret"},
		},
	}
	data := map[string]any{"token": "supplied-secret"}

	err := Validate(schema, data, PhaseDispatch)
	if err == nil {
		t.Fatal("a const mismatch must fail validation")
	}
	msg := err.Error()
	if strings.Contains(msg, "expected-secret") {
		t.Errorf("const error must not embed the declared value, got: %s", msg)
	}
	if strings.Contains(msg, "supplied-secret") {
		t.Errorf("const error must not embed the supplied value, got: %s", msg)
	}
	if !strings.Contains(msg, "/token") {
		t.Errorf("const error must name the failing path, got: %s", msg)
	}
	if !strings.Contains(msg, "const") {
		t.Errorf("const error must name the constraint, got: %s", msg)
	}
}

// @concept: inertness
func TestValidate_NumericAndFormatViolationsOmitTheValue(t *testing.T) {
	cases := []struct {
		name       string
		constraint map[string]any
		value      any
		keyword    string
	}{
		{"minimum", map[string]any{"type": "number", "minimum": 999999}, 4242, "minimum"},
		{"maximum", map[string]any{"type": "number", "maximum": 10}, 4242, "maximum"},
		{"exclusiveMinimum", map[string]any{"type": "number", "exclusiveMinimum": 999999}, 4242, "exclusiveMinimum"},
		{"multipleOf", map[string]any{"type": "number", "multipleOf": 10}, 4242, "multipleOf"},
		{"format", map[string]any{"type": "string", "format": "ipv4"}, "4242-not-an-address", "format"},
		{"enum", map[string]any{"enum": []any{"alpha", "beta"}}, "4242", "enum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := map[string]any{
				"type":       "object",
				"properties": map[string]any{"field": tc.constraint},
			}
			err := Validate(schema, map[string]any{"field": tc.value}, PhaseCommit)
			if err == nil {
				t.Fatalf("%s violation must fail validation", tc.name)
			}
			msg := err.Error()
			if strings.Contains(msg, "4242") {
				t.Errorf("%s error must not embed the value, got: %s", tc.name, msg)
			}
			if !strings.Contains(msg, "/field") {
				t.Errorf("%s error must name the failing path, got: %s", tc.name, msg)
			}
			if !strings.Contains(msg, tc.keyword) {
				t.Errorf("%s error must name the constraint, got: %s", tc.name, msg)
			}
		})
	}
}

// @concept: attribute
func TestValidate_MissingPropertyStillNamesTheProperty(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"required":   []any{"needed"},
		"properties": map[string]any{"needed": map[string]any{"type": "string"}},
	}
	err := Validate(schema, map[string]any{}, PhaseDispatch)
	if err == nil {
		t.Fatal("a missing required property must fail validation")
	}
	if !strings.Contains(err.Error(), "needed") {
		t.Errorf("a required-property error must name the property, got: %s", err.Error())
	}
}
