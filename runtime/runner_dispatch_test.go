// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit tests for runner_dispatch.go::substituteAttributesSchema. New
// in the 2026-05-21 userdata collapse — `substituteAttributesSchema`
// gained responsibility for emitting static-default values (the role
// userdata played pre-collapse). These tests pin the new shape.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	attributes "github.com/rimsky-ai/rimsky-core/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/graph/node"
)

// TestSubstituteAttributesSchema_StaticDefaults — properties without
// `source:` but with `default:` emit the default value in the output.
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
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{})
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["model"], "claude-sonnet-4-5"; got != want {
		t.Fatalf("model default: want %q, got %v", want, got)
	}
}

// TestSubstituteAttributesSchema_EmbeddedSource — source with embedded
// text + directives resolves to a concatenated string.
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
	out, err := substituteAttributesSchema(schema, ctx)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["prompt"], "Hello world"; got != want {
		t.Fatalf("prompt: want %q, got %v", want, got)
	}
}

// TestSubstituteAttributesSchema_LenientNullEmit — `?` marker on a
// missing source emits JSON null, not an error. The property is
// expected to land in the bag with a nil value.
func TestSubstituteAttributesSchema_LenientNullEmit(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"warnings": map[string]any{
				"type":   "string",
				"source": "{{nodes.verify.attribute.warnings_block?}}",
			},
		},
	}
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{Deps: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if v, ok := out["warnings"]; !ok || v != nil {
		t.Fatalf("warnings: want present nil, got (present=%v, value=%v)", ok, v)
	}
}

// TestSubstituteAttributesSchema_ExecutorWrittenOmitted — properties
// with no source and no default are absent from the dispatch bag
// (executor-write-back populates them at commit).
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
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{})
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if _, present := out["summary"]; present {
		t.Fatalf("summary: want absent (executor-write-back), got present=%v", out["summary"])
	}
}

// TestSubstituteAttributesSchema_StrictMissingFailsDispatch — a strict
// (no `?` marker) source directive that misses must fail dispatch with
// ErrMissingSource regardless of whether the property is `required`.
// Per spec §"Resolution waterfall" step 5: "A missing directive with
// no marker fails dispatch with `template_resolution_failed`." Pre-
// userdata-collapse the runtime gated this failure on the `required`
// list; the collapse spec removes that gate.
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
			// `required` deliberately omits "prompt".
		}
		_, err := substituteAttributesSchema(schema, attributes.ResolveContext{})
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
		_, err := substituteAttributesSchema(schema, attributes.ResolveContext{})
		if err == nil {
			t.Fatalf("expected ErrMissingSource for strict-missing required property; got nil")
		}
		if !attributes.IsMissingSource(err) {
			t.Fatalf("expected ErrMissingSource, got %T: %v", err, err)
		}
	})

	t.Run("lenient marker on the same property passes (returns nil)", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":   "string",
					"source": "{{params.absent?}}",
				},
			},
		}
		out, err := substituteAttributesSchema(schema, attributes.ResolveContext{})
		if err != nil {
			t.Fatalf("lenient miss: want nil error, got %v", err)
		}
		if v, ok := out["prompt"]; !ok || v != nil {
			t.Fatalf("lenient miss: want present nil, got (present=%v, value=%v)", ok, v)
		}
	})
}

// TestSubstituteAttributesSchema_MixedShapes — a schema mixing all
// three shapes (source-bound, static-default, executor-written) emits
// the first two and omits the third.
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
	out, err := substituteAttributesSchema(schema, ctx)
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

// TestRelaxRequiredToSourceDriven — the dispatch-time pre-validate
// schema must keep source-bound AND static-default properties in
// `required`, and drop only executor-written (readOnly + no source +
// no default) properties. The cycle-2 fix corrected an earlier
// version that dropped static-default properties incorrectly.
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

// TestResolveAttributes_ExecutorSchemaUnavailable pins the dispatch-time
// gate added by the 2026-05-21 userdata collapse cycle-3 review: when
// the runtime cannot see the executor's `expected_attributes_schema`
// at dispatch (no resolver wired, resolver returns ok=false, or the
// executor advertises empty bytes), `resolveAttributes` must fail loud
// with `executor_schema_unavailable` rather than dispatching with a
// relaxed contract.
//
// The gate fires BEFORE `buildResolveContextForDispatch`, so this test
// can exercise it with a zero-value RunArgs.Persist — we never reach
// the persistence call.
func TestResolveAttributes_ExecutorSchemaUnavailable(t *testing.T) {
	// Acquisition with one node-side property declared. The property has
	// no `source:`, no `default:`, and no `readOnly: true` — its
	// admissibility depends entirely on the executor's expected schema
	// being visible.
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
		// No resolver wired at all — same outcome as resolver returning
		// ok=false. The dispatch gate cannot distinguish "you didn't wire
		// observability" from "your executor doesn't advertise a schema."
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
		// With the permissive `{"type":"object"}` schema visible the gate
		// passes: `computeEffectiveAttributeSchema` reports the schema as
		// visible, and the unified-attribute-surface check sees a
		// permissive executor schema and skips the readOnly-fallback leg.
		// Asserted at the `computeEffectiveAttributeSchema` boundary
		// because `resolveAttributes` proper requires Persist to be
		// non-nil for the post-gate event-append branch.
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
		// Re-run the same gate check resolveAttributes performs, in
		// isolation, to confirm the bypass admits a sourceless+defaultless
		// property under a permissive schema.
		errs := node.CheckEffectiveAttributesSchema(
			schema,
			makeAcq().NodeDef.Attributes.Schema,
			extractReadOnlyPropsLocal(execSchema),
			visible,
			visible && node.IsPermissiveExecutorSchema(execSchema),
		)
		if len(errs) != 0 {
			t.Fatalf("permissive schema: unified-surface check should bypass readOnly leg; got errors: %+v", errs)
		}
	})
}

// TestResolveAttributes_DispatchExecutorSchemaValidation — Gap 1 dispatch
// defense-in-depth (2026-05-21 cycle). When an L4 override (or any
// other resolved value) violates the executor's raw expected_attributes_
// schema at dispatch, the dispatch-time defense pass against the raw
// executor schema must catch the violation and surface it as a typed
// `*attributeValidationError` so applyAttributeFailure routes the
// failure through the `template_validation_failed` policy chain rather
// than the resolution chain.
//
// L4 overrides are shape-blind at instance creation per the structural-
// inertness rule, so the dispatch gate is the first opportunity to
// catch a mistyped override. The relaxed `dispatchSchema` (built from
// the effective schema) catches `required:` violations, but the
// defense-in-depth pass against the executor's raw schema is what
// catches override values that are present but of the wrong type.
//
// The test exercises the validation pipeline directly (skipping
// resolveAttributes' tx-bound substitution path so it can run without
// a real Persist) — apply overrides, then re-validate against the
// executor's raw schema and confirm the typed error.
func TestResolveAttributes_DispatchExecutorSchemaValidation(t *testing.T) {
	executorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model": map[string]any{"type": "string"},
		},
	}
	// Bag the dispatch path would build after source+default+override
	// merge: `model` is an integer, but the executor's schema declares
	// it `string`. The relaxed dispatchSchema (matching what
	// relaxRequiredToSourceDriven would emit) accepts it because the
	// effective schema has no per-property type constraint after the
	// most-specific-wins L2/L1 merge for the test fixture. The defense-
	// in-depth pass against the executor's raw schema rejects it.
	resolved := map[string]any{"model": 42}

	// Wrap the validation error the same way resolveAttributes does —
	// the wrapping is the load-bearing part of the contract for
	// classifyAttributeFailure to route correctly.
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
	if eventKind != "template_validation_failed" {
		t.Fatalf("expected eventKind=template_validation_failed, got %q", eventKind)
	}
}

// TestResolveAttributes_RequiredReadOnlyExecutorWritten pins the fix for
// a false-positive that fired when an executor declared `required:` for
// a `readOnly: true` property. Executor-written properties land in the
// bag at commit (via write-back), not at dispatch — so the dispatch-
// time defense-in-depth pass against the executor's raw schema must
// not enforce `required:` on them. Source-bound and static-default
// `required:` entries continue to be enforced (they are in the dispatch
// bag by construction). Exercises `relaxRequiredForExecutorWritten`
// directly plus the wired-in validation call shape.
func TestResolveAttributes_RequiredReadOnlyExecutorWritten(t *testing.T) {
	executorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt":   map[string]any{"type": "string"},
			"response": map[string]any{"type": "string", "readOnly": true},
		},
		"required": []any{"prompt", "response"},
	}
	// Dispatch bag the substitution pass would produce: `prompt` is
	// populated (source-bound resolved); `response` is absent because
	// the executor will write it at commit.
	resolved := map[string]any{"prompt": "hello"}

	t.Run("relaxRequiredForExecutorWritten drops readOnly required", func(t *testing.T) {
		out := relaxRequiredForExecutorWritten(executorSchema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "prompt" {
			t.Fatalf("relaxed required: want [prompt], got %#v", req)
		}
		// Source schema must be unchanged (no mutation).
		origReq, _ := executorSchema["required"].([]any)
		if len(origReq) != 2 {
			t.Fatalf("source schema mutated: required=%#v", origReq)
		}
	})

	t.Run("validate against raw schema fires false-positive required", func(t *testing.T) {
		// Control: validating directly against the raw executor schema
		// (without the relaxation) is expected to fail with a missing-
		// `response` complaint — that's the false positive the fix
		// avoids.
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
		// Drop `prompt` from the bag — a source-bound `required:` entry
		// must still fire under the relaxed schema (only readOnly-
		// required entries get dropped).
		relaxed := relaxRequiredForExecutorWritten(executorSchema)
		empty := map[string]any{}
		if err := attributes.Validate(relaxed, empty, attributes.PhaseDispatch); err == nil {
			t.Fatalf("expected source-bound `prompt` to remain required after relaxation, got nil")
		}
	})
}

// TestRelaxRequiredForExecutorWritten pins the helper's shape — sibling
// to TestRelaxRequiredToSourceDriven for the executor-raw-schema view.
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
		// Defensive: a malformed schema with `required:` listing a name
		// that has no corresponding `properties` entry should not be
		// dropped — we can't classify it as readOnly without a property
		// declaration. The validator will surface the underlying
		// inconsistency.
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

// TestClassifyAttributeFailure_RoutesByErrorType — Gap 3. Each of the
// three error classes recognised by `applyAttributeFailure` must map to
// its own (error_class, event_kind) pair. Defensive fallback for
// unrecognised error shapes is also pinned (anything we didn't classify
// stays in the resolution chain so existing behaviour is preserved).
func TestClassifyAttributeFailure_RoutesByErrorType(t *testing.T) {
	t.Run("ErrMissingSource → template_resolution_failed", func(t *testing.T) {
		err := &attributes.ErrMissingSource{Directive: "nodes.x.attribute.y", Reason: "missing"}
		class, eventKind := classifyAttributeFailure(err)
		if class != "template_resolution_failed" {
			t.Fatalf("class: want template_resolution_failed, got %q", class)
		}
		if eventKind != "template_resolution_failed" {
			t.Fatalf("eventKind: want template_resolution_failed, got %q", eventKind)
		}
	})

	t.Run("executorSchemaUnavailableError → executor_schema_unavailable", func(t *testing.T) {
		err := &executorSchemaUnavailableError{Executor: "test-executor"}
		class, eventKind := classifyAttributeFailure(err)
		if class != "executor_schema_unavailable" {
			t.Fatalf("class: want executor_schema_unavailable, got %q", class)
		}
		if eventKind != "executor_schema_unavailable" {
			t.Fatalf("eventKind: want executor_schema_unavailable, got %q", eventKind)
		}
	})

	t.Run("attributeValidationError → template_validation_failed", func(t *testing.T) {
		err := &attributeValidationError{Reason: "test failure"}
		class, eventKind := classifyAttributeFailure(err)
		if class != "template_validation_failed" {
			t.Fatalf("class: want template_validation_failed, got %q", class)
		}
		if eventKind != "template_validation_failed" {
			t.Fatalf("eventKind: want template_validation_failed, got %q", eventKind)
		}
	})

	t.Run("unrecognised error falls back to template_resolution_failed", func(t *testing.T) {
		// Defensive: anything that didn't go through resolveAttributes'
		// typed wrappers still routes through the resolution chain so
		// existing call-site assumptions hold.
		err := errors.New("unrecognised")
		class, _ := classifyAttributeFailure(err)
		if class != "template_resolution_failed" {
			t.Fatalf("class: want template_resolution_failed, got %q", class)
		}
	})
}
