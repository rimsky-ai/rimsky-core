// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
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
	out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
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
	out, err := substituteAttributesSchemaWith(schema, ctx, nil, false)
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
	out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{Deps: map[string]json.RawMessage{}}, nil, false)
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
			out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
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
	out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
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
		_, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
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
		_, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
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
		out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
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
	out, err := substituteAttributesSchemaWith(schema, ctx, nil, false)
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

func TestResolveAttributes_DispatchReappliesEffectiveSchemaShapeCheck(t *testing.T) {
	acq := &acquisition{
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
	args := RunArgs{
		ExpectedAttributesSchemaFor: func(executorName string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"x":{"type":"string"}}}`), true
		},
	}
	_, _, err := resolveAttributes(context.Background(), args, acq)
	if err == nil {
		t.Fatalf("expected a dispatch-side effective-schema shape violation, got nil")
	}
	var validation *attributeValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected *attributeValidationError, got %T: %v", err, err)
	}
	if validation.Reason != "dispatch_effective_schema_shape_violation" {
		t.Fatalf("got reason %q, want dispatch_effective_schema_shape_violation", validation.Reason)
	}
	if !strings.Contains(validation.Error(), "properties.x") {
		t.Fatalf("expected error to name property x, got %v", validation)
	}
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
	out, err := substituteAttributesSchemaWith(schema, rctx, carryForward, false)
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
		out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, nil, false)
		if err != nil {
			t.Fatalf("substituteAttributesSchema: %v", err)
		}
		if got, want := out["model"], "claude-sonnet-4-5"; got != want {
			t.Fatalf("first dispatch: want default %q, got %v", want, got)
		}
	})

	t.Run("carry-forward beats default on later dispatches", func(t *testing.T) {
		carryForward := map[string]any{"model": "claude-opus-4-7"}
		out, err := substituteAttributesSchemaWith(schema, attributes.ResolveContext{}, carryForward, false)
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
		out, err := substituteAttributesSchemaWith(schemaRO, attributes.ResolveContext{}, carryForward, false)
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
	db              persistence.Database
	tables          persistence.Tables
	instanceID      shared.UUID
	nodeID          shared.UUID
	callerNodeID    shared.UUID
	mainScopeID     shared.UUID
	subScopeID      shared.UUID
	parentNodeRunID shared.UUID
	frameID         shared.UUID
	templateHash    string
}

func seedCarryForwardFixture(t *testing.T, ctx context.Context) carryForwardFixture {
	t.Helper()
	db := openInMemoryDatabaseForCarryForward(t)
	tables := db.Tables()
	fx := carryForwardFixture{
		db:              db,
		tables:          tables,
		instanceID:      shared.UUID(uuid.New()),
		nodeID:          shared.UUID(uuid.New()),
		callerNodeID:    shared.UUID(uuid.New()),
		mainScopeID:     shared.UUID(uuid.New()),
		subScopeID:      shared.UUID(uuid.New()),
		parentNodeRunID: shared.UUID(uuid.New()),
		templateHash:    "sha256-" + uuid.NewString(),
	}

	tmpl := tmplspec.TemplateSpec{
		Name:    "carry-forward-fixture",
		Version: "1",
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
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         fx.mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: fx.instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
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
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: fx.instanceID,
			Type:       "test/fixture-seed",
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, fx.instanceID, msgID, fx.mainScopeID, tx)
		if err != nil {
			return err
		}
		fx.frameID = frameID
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: fx.parentNodeRunID, NodeID: fx.callerNodeID, FrameID: frameID,
			RunScopeID: fx.mainScopeID,
		}, tx); err != nil {
			return err
		}
		mainScopeIDCopy := fx.mainScopeID
		parentRunIDCopy := fx.parentNodeRunID
		return tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:               fx.subScopeID,
			ParentRunScopeID: &mainScopeIDCopy,
			ParentNodeRunID:  &parentRunIDCopy,
			GraphName:        "inner",
			InstanceID:       fx.instanceID,
		}, tx)
	}); err != nil {
		t.Fatalf("seedCarryForwardFixture: %v", err)
	}
	return fx
}

func makeStatefulCounterAcq(fx carryForwardFixture, scopeID shared.UUID) *acquisition {
	return &acquisition{
		NodeRunID:  shared.UUID(uuid.New()),
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
	nodeRunID := shared.UUID(uuid.New())
	if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := fx.tables.NodeRunTree().CreateChildNodeRun(ctx, persistence.CreateChildNodeRunInput{
			NodeRunID: nodeRunID, NodeID: fx.nodeID, FrameID: fx.frameID,
			RunScopeID: fx.mainScopeID,
		}, tx); err != nil {
			return err
		}
		return fx.tables.NodeAttributes().SetDispatchInputBag(ctx, nodeRunID, fx.nodeID, map[string]any{"count": float64(7)}, tx)
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
	acq.NodeRunID = nodeRunID
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

// @concept: attribute
func TestResolveAttributes_DispatchGateRejectsInvalidResolvedBag(t *testing.T) {
	ctx := context.Background()
	execSchema := []byte(`{"type":"object","properties":{"model":{"type":"string","readOnly":true}}}`)
	nodeSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model": map[string]any{"type": "string", "readOnly": true},
		},
	}

	makeArgs := func(fx carryForwardFixture) RunArgs {
		return RunArgs{
			Persist:      fx.tables,
			Logger:       shared.SilentLogger{},
			Clock:        shared.SystemClock{},
			SupervisorID: "sup-dispatch-gate",
			ExpectedAttributesSchemaFor: func(string) ([]byte, bool) {
				return execSchema, true
			},
		}
	}

	makeAcq := func(fx carryForwardFixture, nodeRunID shared.UUID) *acquisition {
		return &acquisition{
			NodeRunID:  nodeRunID,
			NodeID:     fx.nodeID,
			InstanceID: fx.instanceID,
			NodeType:   "stateful-node",
			Executor:   "test-executor",
			GraphName:  tmplspec.MainGraphName,
			RunScopeID: fx.mainScopeID,
			FrameID:    fx.frameID,
			NodeDef: &node.TemplateNodeDef{
				Type:       "stateful-node",
				Executor:   "test-executor",
				Attributes: &node.NodeAttributesDef{Schema: nodeSchema},
			},
		}
	}

	seedRun := func(t *testing.T, fx carryForwardFixture, bag map[string]any) shared.UUID {
		t.Helper()
		nodeRunID := shared.UUID(uuid.New())
		if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := fx.tables.NodeRunTree().CreateChildNodeRun(ctx, persistence.CreateChildNodeRunInput{
				NodeRunID: nodeRunID, NodeID: fx.nodeID, FrameID: fx.frameID,
				RunScopeID: fx.mainScopeID,
			}, tx); err != nil {
				return err
			}
			return fx.tables.NodeAttributes().SetDispatchInputBag(ctx, nodeRunID, fx.nodeID, bag, tx)
		}); err != nil {
			t.Fatalf("seed run: %v", err)
		}
		return nodeRunID
	}

	t.Run("resolved bag violating effective schema is rejected at dispatch", func(t *testing.T) {
		fx := seedCarryForwardFixture(t, ctx)
		nodeRunID := seedRun(t, fx, map[string]any{"model": float64(42)})
		_, _, err := resolveAttributes(ctx, makeArgs(fx), makeAcq(fx, nodeRunID))
		if err == nil {
			t.Fatalf("expected dispatch-gate rejection for type-mismatched bag, got nil")
		}
		var validation *attributeValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected *attributeValidationError, got %T: %v", err, err)
		}
		if class, _ := classifyAttributeFailure(err); class != "template_validation_failed" {
			t.Fatalf("expected class template_validation_failed, got %q", class)
		}
	})

	t.Run("valid resolved bag dispatches", func(t *testing.T) {
		fx := seedCarryForwardFixture(t, ctx)
		nodeRunID := seedRun(t, fx, map[string]any{"model": "claude-sonnet"})
		resolved, _, err := resolveAttributes(ctx, makeArgs(fx), makeAcq(fx, nodeRunID))
		if err != nil {
			t.Fatalf("resolveAttributes: unexpected error for valid bag: %v", err)
		}
		if got := resolved["model"]; got != "claude-sonnet" {
			t.Fatalf("model: want claude-sonnet, got %v", got)
		}
	})
}

func TestValidateTags_UndeclaredTagRejected(t *testing.T) {
	declared := func(name string) ([]string, bool) {
		if name != "exec-a" {
			return nil, false
		}
		return []string{"approved"}, true
	}
	dctx := dispatchContext{
		Args:     RunArgs{DeclaredTagsFor: declared},
		Acquired: &acquisition{Executor: "exec-a"},
	}

	rejected := validateTags(context.Background(), dctx, terminalEvent{
		Kind:    terminalKindComplete,
		Changed: true,
		Tags:    []string{"approved", "rogue"},
	})
	if rejected.Kind != terminalKindErrored {
		t.Fatalf("Kind = %v, want terminalKindErrored", rejected.Kind)
	}
	if rejected.ErrorClass != "executor_protocol_violation" {
		t.Fatalf("ErrorClass = %q, want executor_protocol_violation", rejected.ErrorClass)
	}
	if rejected.Payload["reason"] != "undeclared_tag" {
		t.Fatalf("Payload[reason] = %v, want undeclared_tag", rejected.Payload["reason"])
	}
	if rejected.Payload["tag"] != "rogue" {
		t.Fatalf("Payload[tag] = %v, want rogue", rejected.Payload["tag"])
	}
	if declaredTags, _ := rejected.Payload["declared_tags"].([]string); len(declaredTags) != 1 || declaredTags[0] != "approved" {
		t.Fatalf("Payload[declared_tags] = %v, want [approved]", rejected.Payload["declared_tags"])
	}

	accepted := validateTags(context.Background(), dctx, terminalEvent{
		Kind:    terminalKindComplete,
		Changed: true,
		Tags:    []string{"approved"},
	})
	if accepted.Kind != terminalKindComplete {
		t.Fatalf("declared tag must pass through unrejected, got Kind=%v", accepted.Kind)
	}
	if len(accepted.Tags) != 1 || accepted.Tags[0] != "approved" {
		t.Fatalf("declared tag must be preserved, got %v", accepted.Tags)
	}

	unknownExecutor := dispatchContext{
		Args:     RunArgs{DeclaredTagsFor: declared},
		Acquired: &acquisition{Executor: "exec-unregistered"},
	}
	passthrough := validateTags(context.Background(), unknownExecutor, terminalEvent{
		Kind: terminalKindComplete,
		Tags: []string{"anything"},
	})
	if passthrough.Kind != terminalKindComplete {
		t.Fatalf("executor with no declared-tags entry must not be rejected, got Kind=%v", passthrough.Kind)
	}
}

type fakeErrorAwareResolver struct {
	err error
}

func (r fakeErrorAwareResolver) Resolve(string, executor.DispatchContext) (executor.Endpoint, bool) {
	return executor.Endpoint{}, false
}

func (r fakeErrorAwareResolver) AcceptedNames() []string { return nil }

func (r fakeErrorAwareResolver) ResolveWithError(string, executor.DispatchContext) (executor.Endpoint, bool, error) {
	return executor.Endpoint{}, false, r.err
}

func TestDispatch_ResolverLookupError_IsRetryableInfraNotTerminalUnresolved(t *testing.T) {
	wantErr := errors.New("binding lookup: transient db error")
	dctx := dispatchContext{
		Args: RunArgs{
			Resolver: fakeErrorAwareResolver{err: wantErr},
			Clock:    shared.SystemClock{},
		},
		Acquired: &acquisition{
			NodeID:     newUUID(),
			InstanceID: newUUID(),
			Executor:   "late-bound-executor",
		},
	}

	ev, result, err := dispatch(context.Background(), dctx)
	if err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no RunnerResult for a resolver lookup failure, got %+v", result)
	}
	if ev.Kind != terminalKindInfra {
		t.Fatalf("Kind = %v, want terminalKindInfra: a transient resolver lookup error must be retryable infra, not the permanent unresolved_executor terminal", ev.Kind)
	}
	if ev.ErrorClass != "executor_resolve_failed" {
		t.Fatalf("ErrorClass = %q, want executor_resolve_failed", ev.ErrorClass)
	}
	if ev.Payload["error"] != wantErr.Error() {
		t.Fatalf("Payload[error] = %v, want %q", ev.Payload["error"], wantErr.Error())
	}
}

type ctxCheckingHandler struct{}

func (ctxCheckingHandler) Execute(ctx context.Context, _ *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &genv1.Outcome{}, nil
}

func TestDispatch_ExecuteContextCanceled_IsInfraDispatchCancelledNotDialFailed(t *testing.T) {
	const execName = "cancel-test-executor"
	reg := executor.NewInProcessRegistry()
	if err := reg.Register("inproc://cancel-test", ctxCheckingHandler{}); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	pool := executor.NewClientPoolWithInProcess(reg, nil)
	resolver := executor.NewStaticResolver(map[string]executor.Endpoint{
		execName: {Transport: "inproc", URL: "inproc://cancel-test"},
	})

	dctx := dispatchContext{
		Args: RunArgs{
			Resolver: resolver,
			Pool:     pool,
			Clock:    shared.SystemClock{},
			Logger:   shared.SilentLogger{},
		},
		Acquired: &acquisition{
			NodeID:     newUUID(),
			NodeRunID:  newUUID(),
			InstanceID: newUUID(),
			Executor:   execName,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ev, result, err := dispatch(ctx, dctx)
	if err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no RunnerResult for a caller-cancelled dispatch, got %+v", result)
	}
	if ev.Kind != terminalKindInfra {
		t.Fatalf("Kind = %v, want terminalKindInfra", ev.Kind)
	}
	if ev.ErrorClass != "executor_dispatch_cancelled" {
		t.Fatalf("ErrorClass = %q, want executor_dispatch_cancelled — a caller-context cancellation "+
			"(e.g. supervisor shutdown) must not be misreported as a dial failure", ev.ErrorClass)
	}
}

// @concept: attribute
func TestBuildExecuteRequest_NonEncodableAttributesFailsClosed(t *testing.T) {
	dctx := dispatchContext{
		Args:       RunArgs{Logger: shared.SilentLogger{}},
		Attributes: map[string]any{"bad": make(chan int)},
		Acquired: &acquisition{
			NodeID:     newUUID(),
			InstanceID: newUUID(),
		},
	}

	_, err := buildExecuteRequest(context.Background(), dctx)
	if err == nil {
		t.Fatal("expected buildExecuteRequest to fail when attributes cannot be encoded as structpb, " +
			"not silently dispatch an empty attributes bag")
	}
}

// @concept: attribute
func TestBuildExecuteRequest_NonEncodableAttributesSchemaFailsClosed(t *testing.T) {
	dctx := dispatchContext{
		Args:             RunArgs{Logger: shared.SilentLogger{}},
		AttributesSchema: map[string]any{"bad": make(chan int)},
		Acquired: &acquisition{
			NodeID:     newUUID(),
			InstanceID: newUUID(),
		},
	}

	_, err := buildExecuteRequest(context.Background(), dctx)
	if err == nil {
		t.Fatal("expected buildExecuteRequest to fail when attributes_schema cannot be encoded as structpb, " +
			"not silently dispatch an empty schema")
	}
}

// @decision: async-callback-persistent-registry
func TestRegisterAsyncIfSet_PropagatesCollisionAsFailure(t *testing.T) {
	dctx := dispatchContext{
		Args: RunArgs{SupervisorID: "sup-1"},
		Acquired: &acquisition{
			NodeID:    newUUID(),
			NodeRunID: newUUID(),
		},
		RegisterAsync: func(string, AsyncContext) bool { return false },
	}
	if ok := registerAsyncIfSet(dctx, "ack-1", ""); ok {
		t.Fatal("registerAsyncIfSet must propagate a collision (RegisterAsync returning false) as failure, " +
			"not swallow it as success")
	}
}

func TestRegisterAsyncIfSet_NilCallbackIsSuccess(t *testing.T) {
	dctx := dispatchContext{
		Args: RunArgs{SupervisorID: "sup-1"},
		Acquired: &acquisition{
			NodeID:    newUUID(),
			NodeRunID: newUUID(),
		},
	}
	if ok := registerAsyncIfSet(dctx, "ack-1", ""); !ok {
		t.Fatal("registerAsyncIfSet with no RegisterAsync callback configured must be a no-op success")
	}
}
