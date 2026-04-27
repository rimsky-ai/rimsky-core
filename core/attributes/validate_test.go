package attributes

import (
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
	if !errAs(err, &ve) {
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
		// subtopic intentionally omitted
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

	// JSON Schema declares `required` must be an array of strings; passing
	// a string instead is a schema-compile error.
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

// errAs is a tiny helper to keep the test imports lean — we already pull in
// the standard errors package transitively via the package under test.
func errAs(err error, target **ErrSchemaValidation) bool {
	for cur := err; cur != nil; {
		if v, ok := cur.(*ErrSchemaValidation); ok {
			*target = v
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := cur.(unwrapper)
		if !ok {
			break
		}
		cur = u.Unwrap()
	}
	return false
}
