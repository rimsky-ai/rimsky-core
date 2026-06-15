// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit tests for runner_dispatch.go::substituteAttributesSchema. New
// in the 2026-05-21 userdata collapse — `substituteAttributesSchema`
// gained responsibility for emitting static-default values (the role
// userdata played pre-collapse). These tests pin the new shape.
//
// Also exercises the 2026-06-14 self-state carry-forward step added to
// resolveAttributes — the pre-substitution hydration of the per-(node,
// RunScope) attribute bag from the most-recent prior writeback. The
// load-bearing property is sub-graph sealing: cross-RunScope hydration
// is forbidden, enforced by GetLatestByNode's JOIN filter (no walk-up
// to the parent scope).

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
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
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
	out, err := substituteAttributesSchema(schema, ctx, nil)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["prompt"], "Hello world"; got != want {
		t.Fatalf("prompt: want %q, got %v", want, got)
	}
}

// TestSubstituteAttributesSchema_LenientEmptyRecovery — `?` marker on a
// missing source recovers to the property's type-appropriate empty
// value (here "" for a string-typed property), not an error and not a
// raw JSON null. Landing a null would fail the downstream PhaseDispatch
// type check for `type: string`, turning the lenient recovery back into
// a hard dispatch failure (story S-template-validation-lenient-marker-
// recovery-e2e). The property lands in the bag with the empty string so
// the executor receives the directive "resolved to empty".
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

// TestSubstituteAttributesSchema_LenientEmptyRecoveryTyped — the
// lenient-recovery empty value matches the property's declared JSON
// type so the PhaseDispatch validation gate admits it for any type, not
// only strings. A bare-string `type`, the union form (`["T","null"]`),
// and an absent `type` all map to a schema-valid empty value.
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
	out, err := substituteAttributesSchema(schema, attributes.ResolveContext{}, nil)
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
			// @deliberate: `required` deliberately omits "prompt".
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
		// @deliberate: The `?` marker recovers to the property's type-appropriate empty
		// value ("" for a string) so the dispatch type check admits it —
		// not a raw null (which would fail `type: string` validation).
		if v, ok := out["prompt"]; !ok || v != "" {
			t.Fatalf("lenient miss: want present empty string, got (present=%v, value=%#v)", ok, v)
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
	// @deliberate: Acquisition with one node-side property declared. The property has
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
		// @deliberate: No resolver wired at all — same outcome as resolver returning
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
		// @deliberate: With the permissive `{"type":"object"}` schema visible the gate
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
		// @deliberate: Re-run the same gate check resolveAttributes performs, in
		// isolation, to confirm the bypass admits a sourceless+defaultless
		// property under a permissive schema.
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
	// @deliberate: Bag the dispatch path would build after source+default+override
	// merge: `model` is an integer, but the executor's schema declares
	// it `string`. The relaxed dispatchSchema (matching what
	// relaxRequiredToSourceDriven would emit) accepts it because the
	// effective schema has no per-property type constraint after the
	// most-specific-wins L2/L1 merge for the test fixture. The defense-
	// in-depth pass against the executor's raw schema rejects it.
	resolved := map[string]any{"model": 42}

	// @deliberate: Wrap the validation error the same way resolveAttributes does —
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
	if eventKind.String() != "template_validation_failed" {
		t.Fatalf("expected eventKind=template_validation_failed, got %q", eventKind.String())
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
	// @deliberate: Dispatch bag the substitution pass would produce: `prompt` is
	// populated (source-bound resolved); `response` is absent because
	// the executor will write it at commit.
	resolved := map[string]any{"prompt": "hello"}

	t.Run("relaxRequiredForExecutorWritten drops readOnly required", func(t *testing.T) {
		out := relaxRequiredForExecutorWritten(executorSchema)
		req, _ := out["required"].([]any)
		if len(req) != 1 || req[0] != "prompt" {
			t.Fatalf("relaxed required: want [prompt], got %#v", req)
		}
		// @deliberate: Source schema must be unchanged (no mutation).
		origReq, _ := executorSchema["required"].([]any)
		if len(origReq) != 2 {
			t.Fatalf("source schema mutated: required=%#v", origReq)
		}
	})

	t.Run("validate against raw schema fires false-positive required", func(t *testing.T) {
		// @deliberate: Control: validating directly against the raw executor schema
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
		// @deliberate: Drop `prompt` from the bag — a source-bound `required:` entry
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
		// @deliberate: Defensive: a malformed schema with `required:` listing a name
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
		// @deliberate: Defensive: anything that didn't go through resolveAttributes'
		// typed wrappers still routes through the resolution chain so
		// existing call-site assumptions hold.
		err := errors.New("unrecognised")
		class, _ := classifyAttributeFailure(err)
		if class != "template_resolution_failed" {
			t.Fatalf("class: want template_resolution_failed, got %q", class)
		}
	})
}

// TestSubstituteAttributesSchema_CarryForward_SourceBoundOverwrites pins
// the spec's "source-bound substitution overlays on top" contract.
// Carry-forward seeds the bag; a source-bound property's substitution
// MUST overwrite the carried value. Cross-node data flow (source
// substitution) and self-state carry-forward are orthogonal channels —
// source-bound is the refresh-from-upstream channel, carry-forward is
// the executor-state channel, and substitution always wins for source-
// bound properties.
//
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
	// Carry-forward holds a stale value for the same property name; the
	// source-bound resolution MUST replace it.
	carryForward := map[string]any{"prompt": "stale carried value"}
	out, err := substituteAttributesSchema(schema, rctx, carryForward)
	if err != nil {
		t.Fatalf("substituteAttributesSchema: %v", err)
	}
	if got, want := out["prompt"], "Generate config"; got != want {
		t.Fatalf("source-bound did not overwrite carry-forward: want %q, got %v", want, got)
	}
}

// TestSubstituteAttributesSchema_CarryForward_DefaultIsFloor pins the
// spec's "static-default is a floor under carry-forward" contract.
// Once a node has produced output in this RunScope, subsequent
// dispatches must see the executor's value rather than the static
// default — otherwise stateful nodes that write to a defaulted property
// (e.g. loop_counter's `count`) would reset on every dispatch.
//
// First dispatch in scope (empty carry-forward) still receives the
// default.
//
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
		// The carry-forward value wins; the static default is the floor
		// under it (only lands when carry-forward has no entry).
		if got, want := out["model"], "claude-opus-4-7"; got != want {
			t.Fatalf("carry-forward did not beat default: want %q, got %v", want, got)
		}
	})

	t.Run("executor-written carry-forward survives unchanged", func(t *testing.T) {
		// Canonical stateful pattern: a readOnly+no-source+no-default
		// property whose carry-forward value must reach the executor
		// (the loop_counter `count` shape). Substitution does NOT touch
		// such properties — the seed survives verbatim.
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

// openInMemoryDatabaseForCarryForward opens a fresh SQLite-backed
// persistence.Database and migrates the schema. The full Database
// handle is returned (not just Tables) so the test can reach Queue —
// the carry-forward SameScope test settles intermediate dispatches via
// Queue.Complete so a fresh run row can be created for the next prior
// writeback (CreateChildRun is idempotent on `(node_id, run_scope_id)`
// while ANY in-flight row exists for the pair, so settling is the only
// way to get two priors). Mirrors openInMemoryTables in
// breakpoint_resume_test but kept private here to avoid coupling test
// files (the breakpoint tests live in `runtime_test` external-package;
// this file is `runtime`).
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

// carryForwardFixture is the persisted shape the carry-forward tests
// run resolveAttributes against. mainScopeID is the calling RunScope;
// subScopeID is a sub-graph RunScope whose parent_run_id points at
// parentRunID in mainScopeID (mirrors the sub-graph sealing topology).
// nodeID is the stateful node whose carry-forward we exercise; it runs
// in BOTH scopes (the carry-forward query keys on (nodeID, scopeID), so
// the same node-id at two scopes is the correct shape). callerNodeID
// is a separate node that anchors parentRunID — using a distinct
// node-id keeps the (nodeID, mainScopeID) pair available for the
// stateful node's prior dispatches (CreateChildRun is idempotent on
// `(node_id, run_scope_id)`, so the calling-node parent run would
// collide with the stateful node's writeback runs otherwise).
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
		Name:                "carry-forward-fixture",
		Version:             "1",
		FrameResolutionMode: tmplspec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
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
		// Main RunScope first, then instance referring to it.
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         fx.mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: fx.instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             fx.instanceID,
			TemplateHash:   fx.templateHash,
			MainRunScopeID: fx.mainScopeID,
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
		frameID, err := tables.Frames().EnqueueSerialFrame(ctx, fx.instanceID, fx.callerNodeID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := tables.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx); err != nil {
			return err
		}
		fx.frameID = frameID
		// A parent run in the main scope on the caller node, used to
		// anchor the sub-graph RunScope's parent_run_id. Keyed on
		// callerNodeID, NOT nodeID, so the (stateful-node, main-scope)
		// idempotency slot stays free for the writeback rows.
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

// seedPriorWriteback inserts a node_run row in `scopeID` for `nodeID`
// and writes `data` to its rimsky_node_attributes row. Mirrors what the
// runtime writes at terminal time, but without dragging the full
// terminal-handler in.
func seedPriorWriteback(
	t *testing.T, ctx context.Context, fx carryForwardFixture,
	scopeID shared.UUID, data map[string]any,
) shared.UUID {
	t.Helper()
	runID := shared.UUID(uuid.New())
	if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := fx.tables.RunTree().CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
			RunID: runID, NodeID: fx.nodeID, FrameID: fx.frameID,
			RunScopeID: scopeID,
		}); err != nil {
			return err
		}
		return fx.tables.NodeAttributes().Upsert(ctx, runID, fx.nodeID, data, tx)
	}); err != nil {
		t.Fatalf("seedPriorWriteback: %v", err)
	}
	return runID
}

// makeStatefulCounterAcq builds an acquisition that mirrors what the
// runtime would build for a stateful "loop_counter" style node — a
// `count: integer, default: 0, readOnly: true` property plus a `max`
// input. Executor is "" so resolveAttributes bypasses the executor-
// schema-visibility gate and the dispatch context lookup; the carry-
// forward step still runs unconditionally for any acquisition with a
// non-nil schema.
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

// TestResolveAttributes_CarryForward_SameScope is the SameScope proof:
// three dispatches of the same node in the same RunScope, where each
// prior dispatch wrote a count value. The third dispatch's pre-
// substitution bag MUST contain the most-recent writeback. This is the
// loop_counter shape — the count field carries forward and increments
// across dispatches.
//
// @story: attribute-carry-forward
// @concept: attribute
func TestResolveAttributes_CarryForward_SameScope(t *testing.T) {
	ctx := context.Background()
	fx := seedCarryForwardFixture(t, ctx)
	// Two prior writebacks in the same RunScope: count=1, then count=2.
	// updated_at ORDER BY DESC in GetLatestByNode picks the most recent
	// row (count=2). The intermediate run must be `Complete`d before the
	// next CreateChildRun so the (node_id, run_scope_id) idempotency
	// guard (in-flight phase) admits a fresh row — mirrors how a
	// production retry / recalculate cycle settles the prior dispatch
	// before the next runs.
	queue := fx.db.Queue()
	run1 := seedPriorWriteback(t, ctx, fx, fx.mainScopeID, map[string]any{"count": float64(1)})
	if err := queue.Complete(ctx, run1, ""); err != nil {
		t.Fatalf("complete prior run: %v", err)
	}
	_ = seedPriorWriteback(t, ctx, fx, fx.mainScopeID, map[string]any{"count": float64(2)})

	args := RunArgs{
		Persist:      fx.tables,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-carry-forward",
	}
	acq := makeStatefulCounterAcq(fx, fx.mainScopeID)
	resolved, schema, err := resolveAttributes(ctx, args, acq)
	if err != nil {
		t.Fatalf("resolveAttributes: %v", err)
	}
	if schema == nil {
		t.Fatalf("expected non-nil schema, got nil")
	}
	// The third dispatch sees count=2 from carry-forward (NOT the schema
	// default of 0). The `max` property has no carry-forward source — it
	// lands at the static default of 3.
	if got := resolved["count"]; got != float64(2) {
		t.Fatalf("count: want carry-forward 2, got %v", got)
	}
	if got := resolved["max"]; got != float64(3) {
		t.Fatalf("max (no carry-forward): want default 3, got %v", got)
	}
}

// TestResolveAttributes_CarryForward_CrossRunScope_Empty is the load-
// bearing sub-graph-sealing proof. A writeback in the main RunScope
// MUST NOT leak into a sub-graph RunScope (different RunScopeID), even
// though the same node_id is at both scopes. The GetLatestByNode JOIN
// filter `r.run_scope_id = $2` enforces this — no walk-up to the parent
// scope. The sub-graph dispatch sees the schema default (count=0).
//
// This is the "fresh context per pass" property the build/validate
// orchestrator depends on; collapsing it would mean a sub-graph's first
// dispatch silently inherited the caller's state.
//
// @story: attribute-carry-forward
// @concept: attribute
func TestResolveAttributes_CarryForward_CrossRunScope_Empty(t *testing.T) {
	ctx := context.Background()
	fx := seedCarryForwardFixture(t, ctx)
	// Seed a writeback in the MAIN RunScope; the sub-graph dispatch
	// below must NOT see it (sub-graph sealing).
	_ = seedPriorWriteback(t, ctx, fx, fx.mainScopeID, map[string]any{"count": float64(5)})

	args := RunArgs{
		Persist:      fx.tables,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-carry-forward",
	}
	// Dispatch in the SUB-graph RunScope. Carry-forward hydration lookup
	// keys on (nodeID, subScopeID) and the JOIN filter excludes the
	// main-scope row written above. The bag falls through to the schema
	// default.
	acq := makeStatefulCounterAcq(fx, fx.subScopeID)
	resolved, _, err := resolveAttributes(ctx, args, acq)
	if err != nil {
		t.Fatalf("resolveAttributes: %v", err)
	}
	if got := resolved["count"]; got != float64(0) {
		t.Fatalf("cross-RunScope hydration LEAK: count carried from main scope; want default 0, got %v", got)
	}
}
