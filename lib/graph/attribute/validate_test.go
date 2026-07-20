// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attribute

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate_HappyPath(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"area":     map[string]any{"type": "string"},
			"subtopic": map[string]any{"type": "string"},
			"count":    map[string]any{"type": "integer"},
		},
		"required": []any{"area", "subtopic"},
	}
	data := map[string]any{
		"area":     "northwest",
		"subtopic": "sea-otters",
		"count":    3,
	}
	if err := Validate(schema, data, PhaseDispatch); err != nil {
		t.Fatalf("expected validation to pass, got %v", err)
	}
}

func TestValidate_TypeMismatch(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	data := map[string]any{
		"count": "not-an-int",
	}
	err := Validate(schema, data, PhaseCommit)
	if err == nil {
		t.Fatalf("expected type mismatch failure")
	}
	if !IsSchemaValidation(err) {
		t.Fatalf("expected ErrSchemaValidation, got %T", err)
	}
	var ve *ErrSchemaValidation
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As failed")
	}
	if ve.Phase != PhaseCommit {
		t.Fatalf("phase: want %q got %q", PhaseCommit, ve.Phase)
	}
	if ve.Cause == nil {
		t.Fatalf("expected wrapped cause from jsonschema")
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"area":     map[string]any{"type": "string"},
			"subtopic": map[string]any{"type": "string"},
		},
		"required": []any{"area", "subtopic"},
	}
	data := map[string]any{
		"area": "northwest",
	}
	err := Validate(schema, data, PhaseDispatch)
	if err == nil {
		t.Fatalf("expected required-missing failure")
	}
	if !IsSchemaValidation(err) {
		t.Fatalf("expected ErrSchemaValidation, got %T", err)
	}
	if !strings.Contains(err.Error(), "phase=dispatch") {
		t.Fatalf("expected phase in error message, got %q", err.Error())
	}
}

func TestValidate_BadSchema(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type":     "object",
		"required": "not-an-array",
	}
	data := map[string]any{}
	err := Validate(schema, data, PhaseDispatch)
	if err == nil {
		t.Fatalf("expected compile failure")
	}
	if !IsSchemaValidation(err) {
		t.Fatalf("expected ErrSchemaValidation, got %T", err)
	}
}

func TestValidate_CompiledSchemaCache_HitsOnRepeatedCalls(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	data := map[string]any{"count": float64(1)}

	before := schemaCompileCount.Load()
	if err := Validate(schema, data, PhaseDispatch); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	afterFirst := schemaCompileCount.Load()
	if afterFirst != before+1 {
		t.Fatalf("first call: want exactly one compile, got delta %d", afterFirst-before)
	}

	for i := 0; i < 5; i++ {
		if err := Validate(schema, data, PhaseCommit); err != nil {
			t.Fatalf("repeat call %d: unexpected error: %v", i, err)
		}
	}
	afterRepeats := schemaCompileCount.Load()
	if afterRepeats != afterFirst {
		t.Fatalf("repeated calls with the same schema recompiled: want %d compiles total, got %d", afterFirst-before, afterRepeats-before)
	}
}

func TestValidate_CompiledSchemaCache_NoCrossContaminationBetweenSchemas(t *testing.T) {
	schemaA := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"count"},
	}
	schemaB := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}

	if err := Validate(schemaA, map[string]any{"count": float64(1)}, PhaseDispatch); err != nil {
		t.Fatalf("schemaA valid data: unexpected error: %v", err)
	}
	if err := Validate(schemaB, map[string]any{"name": "x"}, PhaseDispatch); err != nil {
		t.Fatalf("schemaB valid data: unexpected error: %v", err)
	}
	if err := Validate(schemaA, map[string]any{"name": "x"}, PhaseDispatch); err == nil {
		t.Fatalf("schemaA against schemaB-shaped data: expected failure (missing required 'count'), got nil")
	}
	if err := Validate(schemaB, map[string]any{"count": float64(1)}, PhaseDispatch); err == nil {
		t.Fatalf("schemaB against schemaA-shaped data: expected failure (missing required 'name'), got nil")
	}
}

func TestValidate_WholeDirectiveLift(t *testing.T) {
	t.Parallel()

	t.Run("whole-object pull validates", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"config": map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				},
			},
		}
		data := map[string]any{
			"config": map[string]any{"a": float64(1)},
		}
		if err := Validate(schema, data, PhaseDispatch); err != nil {
			t.Fatalf("expected validation to pass for whole-object lift, got %v", err)
		}
	})

	t.Run("type mismatch (string lifted into object slot) fails", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"config": map[string]any{"type": "object"},
			},
		}
		data := map[string]any{
			"config": "not-an-object",
		}
		err := Validate(schema, data, PhaseDispatch)
		if err == nil {
			t.Fatalf("expected schema validation failure for type mismatch")
		}
		if !IsSchemaValidation(err) {
			t.Fatalf("expected ErrSchemaValidation, got %T", err)
		}
	})

	t.Run("integer lift validates against type:integer", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"count": map[string]any{"type": "integer"},
			},
		}
		data := map[string]any{"count": float64(42)}
		if err := Validate(schema, data, PhaseDispatch); err != nil {
			t.Fatalf("expected validation to pass for integer lift, got %v", err)
		}
	})
}
