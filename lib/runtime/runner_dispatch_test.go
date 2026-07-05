// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestSubstituteAttributesSchema_StaticDefaults(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model": map[string]any{
				"type":    "string",
				"default": "claude-sonnet-4-5",
			},
		},
	}
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["model"], "claude-sonnet-4-5"; got != want {
		t.Fatalf("model default: want %q, got %v", want, got)
	}
}

func TestSubstituteAttributesSchema_EmbeddedSource(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":   "string",
				"source": "Hello {{params.name}}",
			},
		},
	}
	ctx := attributes.ResolveContext{Params: json.RawMessage(`{"name": "world"}`)}
	out, err := substituteAttributesSchema(schema, ctx, nil)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["prompt"], "Hello world"; got != want {
		t.Fatalf("prompt: want %q, got %v", want, got)
	}
}

func TestSubstituteAttributesSchema_LenientEmptyRecovery(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"warnings": map[string]any{
				"type":   "string",
				"source": "{{nodes.verify.attribute.warnings_block?}}",
			},
		},
	}
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{Deps: map[string]json.RawMessage{}}, nil)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if v, ok := out["warnings"]; !ok || v != "" {
		t.Fatalf("warnings: want present empty string, got (present=%v, value=%#v)", ok, v)
	}
}

func TestSubstituteAttributesSchema_LenientEmptyRecoveryTyped(t *testing.T) {
	cases := []struct {
		name      string
		typeField any
		want      any
	}{
		{"string", "string", ""},
		{"number", "number", float64(0)},
		{"integer", "integer", float64(0)},
		{"boolean", "boolean", false},
		{"array", "array", []any{}},
		{"object", "object", map[string]any{}},
		{"union with null", []any{"null", "number"}, float64(0)},
		{"absent type defaults to empty string", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prop := map[string]any{"source": "{{params.absent?}}"}
			if tc.typeField != nil {
				prop["type"] = tc.typeField
			}
			schema := map[string]any{
				"type":       "object",
				"properties": map[string]any{"v": prop},
			}
			out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
			if err != nil {
				t.Fatalf("lenient miss (%s): want nil error, got %v", tc.name, err)
			}
			v, ok := out["v"]
			if !ok {
				t.Fatalf("lenient miss (%s): property absent from bag", tc.name)
			}
			if !reflect.DeepEqual(v, tc.want) {
				t.Fatalf("lenient miss (%s): want %#v, got %#v", tc.name, tc.want, v)
			}
		})
	}
}

func TestSubstituteAttributesSchema_ExecutorWrittenOmitted(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{
				"type":     "string",
				"readOnly": true,
			},
		},
	}
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if _, present := out["summary"]; present {
		t.Fatalf("summary: want absent (executor-write-back), got present=%v", out["summary"])
	}
}

func TestSubstituteAttributesSchema_StrictMissingFailsDispatch(t *testing.T) {
	t.Run("non-required strict miss still fails", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":   "string",
					"source": "{{params.absent}}",
				},
			},
		}
		_, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
		if err == nil {
			t.Fatalf("expected ErrMissingSource for strict-missing non-required property; got nil")
		}
		if !attributes.IsMissingSource(err) {
			t.Fatalf("expected ErrMissingSource, got %T: %v", err, err)
		}
	})

	t.Run("required strict miss also fails", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":   "string",
					"source": "{{params.absent}}",
				},
			},
			"required": []any{"prompt"},
		}
		_, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
		if err == nil {
			t.Fatalf("expected ErrMissingSource for strict-missing required property; got nil")
		}
		if !attributes.IsMissingSource(err) {
			t.Fatalf("expected ErrMissingSource, got %T: %v", err, err)
		}
	})

	t.Run("lenient marker on the same property recovers to empty", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":   "string",
					"source": "{{params.absent?}}",
				},
			},
		}
		out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
		if err != nil {
			t.Fatalf("lenient miss: want nil error, got %v", err)
		}
		if v, ok := out["prompt"]; !ok || v != "" {
			t.Fatalf("lenient miss: want present empty string, got (present=%v, value=%#v)", ok, v)
		}
	})
}

func TestSubstituteAttributesSchema_MixedShapes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":   "string",
				"source": "Generate {{params.what}}",
			},
			"model": map[string]any{
				"type":    "string",
				"default": "claude-sonnet-4-5",
			},
			"response": map[string]any{
				"type":     "string",
				"readOnly": true,
			},
		},
	}
	ctx := attributes.ResolveContext{Params: json.RawMessage(`{"what": "config"}`)}
	out, err := substituteAttributesSchema(schema, ctx, nil)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	want := map[string]any{
		"prompt": "Generate config",
		"model":  "claude-sonnet-4-5",
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("mixed shapes: want %#v, got %#v", want, out)
	}
}

func TestRelaxRequiredToSourceDriven(t *testing.T) {
	t.Run("static-default property stays in required", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"model"},
			"properties": map[string]any{
				"model": map[string]any{
					"type":    "string",
					"default": "X",
				},
			},
		}
		out := relaxRequiredToSourceDriven(schema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "model" {
			t.Fatalf("static-default in required: want [model], got %#v", req)
		}
	})

	t.Run("executor-written (readOnly, no source/default) property dropped from required", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"response"},
			"properties": map[string]any{
				"response": map[string]any{
					"type":     "string",
					"readOnly": true,
				},
			},
		}
		out := relaxRequiredToSourceDriven(schema)
		req, _ := out["required"].([]any)
		if len(req) != 0 {
			t.Fatalf("executor-written in required: want [], got %#v", req)
		}
	})

	t.Run("source-bound property stays in required", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"prompt"},
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":   "string",
					"source": "{{params.what}}",
				},
			},
		}
		out := relaxRequiredToSourceDriven(schema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "prompt" {
			t.Fatalf("source-bound in required: want [prompt], got %#v", req)
		}
	})

	t.Run("mixed: keep source-bound + static-default, drop executor-written", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"prompt", "model", "response"},
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":   "string",
					"source": "{{params.what}}",
				},
				"model": map[string]any{
					"type":    "string",
					"default": "X",
				},
				"response": map[string]any{
					"type":     "string",
					"readOnly": true,
				},
			},
		}
		out := relaxRequiredToSourceDriven(schema)
		req, _ := out["required"].([]any)
		got := map[string]bool{}
		for _, item := range req {
			if name, ok := item.(string); ok {
				got[name] = true
			}
		}
		want := map[string]bool{"prompt": true, "model": true}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mixed required: want %#v, got %#v", want, got)
		}
	})

	t.Run("schema with no required list is returned unchanged", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x": map[string]any{"type": "string"},
			},
		}
		out := relaxRequiredToSourceDriven(schema)
		if _, exists := out["required"]; exists {
			t.Fatalf("expected no required key, got: %#v", out["required"])
		}
	})

	t.Run("nil schema returns nil", func(t *testing.T) {
		if out := relaxRequiredToSourceDriven(nil); out != nil {
			t.Fatalf("nil schema: want nil, got %#v", out)
		}
	})
}

func TestResolveAttributes_ExecutorSchemaUnavailable(t *testing.T) {
	makeAcq := func() *acquisition {
		return &acquisition{
			Executor:  "test-executor",
			GraphName: "main",
			NodeDef: &node.TemplateNodeDef{
				Type:     "a",
				Executor: "test-executor",
				Attributes: &node.NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string"},
					},
				}},
			},
		}
	}

	t.Run("missing schema fails dispatch with executor_schema_unavailable", func(t *testing.T) {
		args := RunArgs{
			ExpectedAttributesSchemaFor: func(executorName string) ([]byte, bool) {
				return nil, false
			},
		}
		_, _, err := resolveAttributes(context.Background(), args, makeAcq())
		if err == nil {
			t.Fatalf("expected executor_schema_unavailable error, got nil")
		}
		if !strings.Contains(err.Error(), "executor_schema_unavailable") {
			t.Fatalf("error message missing `executor_schema_unavailable`: %v", err)
		}
		if !strings.Contains(err.Error(), "test-executor") {
			t.Fatalf("error message missing executor name `test-executor`: %v", err)
		}
	})

	t.Run("nil resolver fails dispatch with executor_schema_unavailable", func(t *testing.T) {
		args := RunArgs{}
		_, _, err := resolveAttributes(context.Background(), args, makeAcq())
		if err == nil {
			t.Fatalf("expected executor_schema_unavailable error, got nil")
		}
		if !strings.Contains(err.Error(), "executor_schema_unavailable") {
			t.Fatalf("error message missing `executor_schema_unavailable`: %v", err)
		}
	})

	t.Run("permissive schema visible — gate does NOT fire", func(t *testing.T) {
		args := RunArgs{
			ExpectedAttributesSchemaFor: func(executorName string) ([]byte, bool) {
				return []byte(`{"type":"object"}`), true
			},
		}
		schema, execSchema, visible := computeEffectiveAttributeSchema(args, makeAcq())
		if !visible {
			t.Fatalf("permissive schema: expected visible=true, got false")
		}
		if schema == nil {
			t.Fatalf("permissive schema: expected non-nil merged schema, got nil")
		}
		if !node.IsPermissiveExecutorSchema(execSchema) {
			t.Fatalf("permissive schema: expected IsPermissiveExecutorSchema to return true for %#v", execSchema)
		}
		errs := node.CheckEffectiveAttributesSchema(
			schema,
			makeAcq().NodeDef.Attributes.Schema,
			execSchema,
			extractReadOnlyPropsLocal(execSchema),
			visible,
		)
		if len(errs) != 0 {
			t.Fatalf("permissive schema: unified-surface check should bypass readOnly leg; got errors: %+v", errs)
		}
	})
}

func TestResolveAttributes_DispatchExecutorSchemaValidation(t *testing.T) {
	executorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model": map[string]any{"type": "string"},
		},
	}
	resolved := map[string]any{"model": 42}

	rawErr := attributes.Validate(executorSchema, resolved, attributes.PhaseDispatch)
	if rawErr == nil {
		t.Fatalf("expected raw executor-schema validation to fail for int-vs-string mismatch")
	}
	wrapped := &attributeValidationError{
		Reason: "dispatch_bag_violates_executor_schema",
		Cause:  rawErr,
	}
	var validation *attributeValidationError
	if !errors.As(error(wrapped), &validation) {
		t.Fatalf("errors.As: expected *attributeValidationError, got %T", wrapped)
	}
	class, eventKind := classifyAttributeFailure(wrapped)
	if class != "template_validation_failed" {
		t.Fatalf("expected class=template_validation_failed, got %q", class)
	}
	if eventKind.String() != "template_validation_failed" {
		t.Fatalf("expected eventKind=template_validation_failed, got %q", eventKind.String())
	}
}

func TestResolveAttributes_RequiredReadOnlyExecutorWritten(t *testing.T) {
	executorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt":   map[string]any{"type": "string"},
			"response": map[string]any{"type": "string", "readOnly": true},
		},
		"required": []any{"prompt", "response"},
	}
	resolved := map[string]any{"prompt": "hello"}

	t.Run("relaxRequiredForExecutorWritten drops readOnly required", func(t *testing.T) {
		out := relaxRequiredForExecutorWritten(executorSchema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "prompt" {
			t.Fatalf("relaxed required: want [prompt], got %#v", req)
		}
		origReq, _ := executorSchema["required"].([]any)
		if len(origReq) != 2 {
			t.Fatalf("source schema mutated: required=%#v", origReq)
		}
	})

	t.Run("validate against raw schema fires false-positive required", func(t *testing.T) {
		err := attributes.Validate(executorSchema, resolved, attributes.PhaseDispatch)
		if err == nil {
			t.Fatalf("raw schema validation should fail (response is required but absent); got nil")
		}
	})

	t.Run("validate against relaxed schema accepts the partial bag", func(t *testing.T) {
		relaxed := relaxRequiredForExecutorWritten(executorSchema)
		if err := attributes.Validate(relaxed, resolved, attributes.PhaseDispatch); err != nil {
			t.Fatalf("relaxed schema validation must accept the dispatch bag missing readOnly props; got %v", err)
		}
	})

	t.Run("source-bound required stays enforced", func(t *testing.T) {
		relaxed := relaxRequiredForExecutorWritten(executorSchema)
		empty := map[string]any{}
		if err := attributes.Validate(relaxed, empty, attributes.PhaseDispatch); err == nil {
			t.Fatalf("expected source-bound `prompt` to remain required after relaxation, got nil")
		}
	})
}

func TestRelaxRequiredForExecutorWritten(t *testing.T) {
	t.Run("readOnly property dropped from required", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"response"},
			"properties": map[string]any{
				"response": map[string]any{
					"type":     "string",
					"readOnly": true,
				},
			},
		}
		out := relaxRequiredForExecutorWritten(schema)
		if _, exists := out["required"]; exists {
			t.Fatalf("expected required key removed when empty, got %#v", out["required"])
		}
	})

	t.Run("non-readOnly property stays in required", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"prompt"},
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string"},
			},
		}
		out := relaxRequiredForExecutorWritten(schema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "prompt" {
			t.Fatalf("non-readOnly required: want [prompt], got %#v", req)
		}
	})

	t.Run("mixed: keep input-required, drop readOnly-required", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"prompt", "model", "response"},
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string"},
				"model":  map[string]any{"type": "string"},
				"response": map[string]any{
					"type":     "string",
					"readOnly": true,
				},
			},
		}
		out := relaxRequiredForExecutorWritten(schema)
		req, _ := out["required"].([]any)
		got := map[string]bool{}
		for _, item := range req {
			if name, ok := item.(string); ok {
				got[name] = true
			}
		}
		want := map[string]bool{"prompt": true, "model": true}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mixed required: want %#v, got %#v", want, got)
		}
	})

	t.Run("schema with no required list is returned unchanged", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x": map[string]any{"type": "string"},
			},
		}
		out := relaxRequiredForExecutorWritten(schema)
		if _, exists := out["required"]; exists {
			t.Fatalf("expected no required key, got: %#v", out["required"])
		}
	})

	t.Run("required entry referring to undeclared property stays", func(t *testing.T) {
		schema := map[string]any{
			"type":       "object",
			"required":   []any{"missing"},
			"properties": map[string]any{},
		}
		out := relaxRequiredForExecutorWritten(schema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "missing" {
			t.Fatalf("unknown-property required: want [missing], got %#v", req)
		}
	})

	t.Run("nil schema returns nil", func(t *testing.T) {
		if out := relaxRequiredForExecutorWritten(nil); out != nil {
			t.Fatalf("nil schema: want nil, got %#v", out)
		}
	})
}

func TestClassifyAttributeFailure_RoutesByErrorType(t *testing.T) {
	t.Run("ErrMissingSource → template_resolution_failed", func(t *testing.T) {
		err := &attributes.ErrMissingSource{Directive: "nodes.x.attribute.y", Reason: "missing"}
		class, eventKind := classifyAttributeFailure(err)
		if class != "template_resolution_failed" {
			t.Fatalf("class: want template_resolution_failed, got %q", class)
		}
		if eventKind.String() != "template_resolution_failed" {
			t.Fatalf("eventKind: want template_resolution_failed, got %q", eventKind.String())
		}
	})

	t.Run("executorSchemaUnavailableError → executor_schema_unavailable", func(t *testing.T) {
		err := &executorSchemaUnavailableError{Executor: "test-executor"}
		class, eventKind := classifyAttributeFailure(err)
		if class != "executor_schema_unavailable" {
			t.Fatalf("class: want executor_schema_unavailable, got %q", class)
		}
		if eventKind.String() != "executor_schema_unavailable" {
			t.Fatalf("eventKind: want executor_schema_unavailable, got %q", eventKind.String())
		}
	})

	t.Run("attributeValidationError → template_validation_failed", func(t *testing.T) {
		err := &attributeValidationError{Reason: "test failure"}
		class, eventKind := classifyAttributeFailure(err)
		if class != "template_validation_failed" {
			t.Fatalf("class: want template_validation_failed, got %q", class)
		}
		if eventKind.String() != "template_validation_failed" {
			t.Fatalf("eventKind: want template_validation_failed, got %q", eventKind.String())
		}
	})

	t.Run("unrecognised error falls back to template_resolution_failed", func(t *testing.T) {
		err := errors.New("unrecognised")
		class, _ := classifyAttributeFailure(err)
		if class != "template_resolution_failed" {
			t.Fatalf("class: want template_resolution_failed, got %q", class)
		}
	})
}

// @story: attribute-carry-forward
// @concept: attribute
func TestSubstituteAttributesSchema_CarryForward_SourceBoundOverwrites(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":   "string",
				"source": "Generate {{params.what}}",
			},
		},
	}
	rctx := attributes.ResolveContext{Params: json.RawMessage(`{"what": "config"}`)}
	carryForward := map[string]any{"prompt": "stale carried value"}
	out, err := substituteAttributesSchema(schema, rctx, carryForward)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["prompt"], "Generate config"; got != want {
		t.Fatalf("source-bound did not overwrite carry-forward: want %q, got %v", want, got)
	}
}

// @story: attribute-carry-forward
// @concept: attribute
func TestSubstituteAttributesSchema_CarryForward_DefaultIsFloor(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model": map[string]any{
				"type":    "string",
				"default": "claude-sonnet-4-5",
			},
		},
	}

	t.Run("first dispatch in scope sees default", func(t *testing.T) {
		out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
		if err != nil {
			t.Fatalf("substituteAttributesSchema: %v", err)
		}
		if got, want := out["model"], "claude-sonnet-4-5"; got != want {
			t.Fatalf("first dispatch: want default %q, got %v", want, got)
		}
	})

	t.Run("carry-forward beats default on later dispatches", func(t *testing.T) {
		carryForward := map[string]any{"model": "claude-opus-4-7"}
		out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, carryForward)
		if err != nil {
			t.Fatalf("substituteAttributesSchema: %v", err)
		}
		if got, want := out["model"], "claude-opus-4-7"; got != want {
			t.Fatalf("carry-forward did not beat default: want %q, got %v", want, got)
		}
	})

	t.Run("executor-written carry-forward survives unchanged", func(t *testing.T) {
		schemaRO := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"count": map[string]any{
					"type":     "integer",
					"readOnly": true,
				},
			},
		}
		carryForward := map[string]any{"count": float64(2)}
		out, err := substituteAttributesSchema(schemaRO, attributes.ResolveContext{}, carryForward)
		if err != nil {
			t.Fatalf("substituteAttributesSchema: %v", err)
		}
		if got := out["count"]; got != float64(2) {
			t.Fatalf("executor-written carry-forward: want 2, got %v", got)
		}
	})
}

func openInMemoryDatabaseForCarryForward(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

type carryForwardFixture struct {
	db           persistence.Database
	tables       persistence.Tables
	instanceID   shared.UUID
	nodeID       shared.UUID
	callerNodeID shared.UUID
	mainScopeID  shared.UUID
	subScopeID   shared.UUID
	parentRunID  shared.UUID
	frameID      shared.UUID
	templateHash string
}

func seedCarryForwardFixture(t *testing.T, ctx context.Context) carryForwardFixture {
	t.Helper()
	db := openInMemoryDatabaseForCarryForward(t)
	tables := db.Tables()
	fx := carryForwardFixture{
		db:           db,
		tables:       tables,
		instanceID:   shared.UUID(uuid.New()),
		nodeID:       shared.UUID(uuid.New()),
		callerNodeID: shared.UUID(uuid.New()),
		mainScopeID:  shared.UUID(uuid.New()),
		subScopeID:   shared.UUID(uuid.New()),
		parentRunID:  shared.UUID(uuid.New()),
		templateHash: "sha256-" + uuid.NewString(),
	}

	tmpl := tmplspec.TemplateSpec{
		Name:           "carry-forward-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []tmplspec.TemplateNodeDef{
			{Type: "stateful-node", Executor: ""},
			{Type: "caller", Executor: ""},
		},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     fx.templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         fx.mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: fx.instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           fx.instanceID,
			TemplateHash: fx.templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: fx.nodeID, InstanceID: fx.instanceID,
			NodeType: "stateful-node",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: fx.callerNodeID, InstanceID: fx.instanceID,
			NodeType: "caller",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: fx.instanceID,
			Type:       "test/fixture-seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, fx.instanceID, msgID, fx.mainScopeID, 600000, tx)
		if err != nil {
			return err
		}
		fx.frameID = frameID
		if err := tables.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID: fx.parentRunID, NodeID: fx.callerNodeID, FrameID: frameID,
			RunScopeID: fx.mainScopeID,
		}); err != nil {
			return err
		}
		mainScopeIDCopy := fx.mainScopeID
		parentRunIDCopy := fx.parentRunID
		return tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               fx.subScopeID,
			ParentRunScopeID: &mainScopeIDCopy,
			ParentRunID:      &parentRunIDCopy,
			GraphName:        "inner",
			InstanceID:       fx.instanceID,
		})
	}); err != nil {
		t.Fatalf("seedCarryForwardFixture: %v", err)
	}
	return fx
}

func makeStatefulCounterAcq(fx carryForwardFixture, scopeID shared.UUID) *acquisition {
	return &acquisition{
		DispatchID: shared.UUID(uuid.New()),
		NodeID:     fx.nodeID,
		InstanceID: fx.instanceID,
		NodeType:   "stateful-node",
		Executor:   "",
		GraphName:  tmplspec.MainGraphName,
		RunScopeID: scopeID,
		FrameID:    fx.frameID,
		NodeDef: &node.TemplateNodeDef{
			Type:     "stateful-node",
			Executor: "",
			Attributes: &node.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{
						"type":     "integer",
						"default":  float64(0),
						"readOnly": true,
					},
					"max": map[string]any{
						"type":    "integer",
						"default": float64(3),
					},
				},
			}},
		},
	}
}

// @story: attribute-carry-forward
// @concept: attribute
// @decision: walker-rule-per-sender-node
// @decision: non-cascade-direct-to-stale
func TestResolveAttributes_LoadsSnapshotBag(t *testing.T) {
	ctx := context.Background()
	fx := seedCarryForwardFixture(t, ctx)
	dispatchID := shared.UUID(uuid.New())
	if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := fx.tables.RunTree().CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
			RunID: dispatchID, NodeID: fx.nodeID, FrameID: fx.frameID,
			RunScopeID: fx.mainScopeID,
		}); err != nil {
			return err
		}
		return fx.tables.NodeAttributes().SetDispatchInputBag(ctx, tx, dispatchID, fx.nodeID,
			map[string]any{"count": float64(7)})
	}); err != nil {
		t.Fatalf("seed dispatch row: %v", err)
	}

	args := RunArgs{
		Persist:      fx.tables,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-carry-forward",
	}
	acq := makeStatefulCounterAcq(fx, fx.mainScopeID)
	acq.DispatchID = dispatchID
	resolved, schema, err := resolveAttributes(ctx, args, acq)
	if err != nil {
		t.Fatalf("resolveAttributes: %v", err)
	}
	if schema == nil {
		t.Fatalf("expected non-nil schema, got nil")
	}
	if got := resolved["count"]; got != float64(7) {
		t.Fatalf("count: want snapshot value 7, got %v", got)
	}
}

// @story: attribute-carry-forward
// @concept: attribute
func TestResolveAttributes_MissingSnapshotIsInvariantViolation(t *testing.T) {
	ctx := context.Background()
	fx := seedCarryForwardFixture(t, ctx)

	args := RunArgs{
		Persist:      fx.tables,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-carry-forward",
	}
	acq := makeStatefulCounterAcq(fx, fx.mainScopeID)
	_, _, err := resolveAttributes(ctx, args, acq)
	if err == nil {
		t.Fatalf("expected invariant-violation error when no dispatch_input_bag is present; got nil")
	}
	if !strings.Contains(err.Error(), "dispatch_input_bag missing") {
		t.Fatalf("expected `dispatch_input_bag missing` error, got %v", err)
	}
}
