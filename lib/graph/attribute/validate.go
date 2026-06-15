// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Validate: JSON Schema validation against the template-declared
// attribute schema, using github.com/santhosh-tekuri/jsonschema/v5 (draft-07
// by default). Spec §5.7.1.
//
// @blessed-invariant 12 — Attributes validate twice.
//
// Both gates are mandatory:
//
//  1. At dispatch, after substitution, the populated attribute object must
//     validate against the schema. Failure raises template_resolution_failed
//     (the supervisor maps the typed error). Required source-driven fields
//     missing here are the dominant case; the JSON Schema `required` keyword
//     is what catches them.
//
//  2. At commit, after the executor's writeback is merged, the populated
//     attribute object must validate again. Failure raises
//     attributes_schema_failed.
//
// This file exposes one entry point — Validate — and the caller picks the
// gate. Removing either call site is a regression of invariant 12.
// (Spec §4.10 invariant 12.)

package attributes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ErrSchemaValidation is the typed error returned by Validate when the data
// does not satisfy the schema, OR when the schema itself is malformed.
// Callers — the supervisor's policy chain — branch on the Phase field to
// route the failure: dispatch-time failures map to
// `template_resolution_failed`, commit-time failures map to
// `attributes_schema_failed` (spec §5.7.1).
//
// Phase is supplied by the caller via Validate's `phase` argument; the
// helper does not infer it from the schema or data.
type ErrSchemaValidation struct {
	Phase   string
	Message string
	// Cause is the underlying jsonschema.ValidationError or schema-compile
	// error. Errors.Is/Errors.As-able for callers that want to inspect
	// keyword locations, etc.
	Cause error
}

func (e *ErrSchemaValidation) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("attributes: schema validation failed (phase=%s): %s: %v", e.Phase, e.Message, e.Cause)
	}
	return fmt.Sprintf("attributes: schema validation failed (phase=%s): %s", e.Phase, e.Message)
}

func (e *ErrSchemaValidation) Unwrap() error { return e.Cause }

// IsSchemaValidation reports whether err is (or wraps) an
// ErrSchemaValidation.
func IsSchemaValidation(err error) bool {
	var v *ErrSchemaValidation
	return errors.As(err, &v)
}

// @deliberate: PhaseDispatch and PhaseCommit are the two recognised phases. Validate
// does not enforce that the caller passes one of these — the field is
// free-form for forward compatibility — but events emitted from the
// supervisor expect these literal strings.
const (
	PhaseDispatch = "dispatch"
	PhaseCommit   = "commit"
)

// Validate runs schema validation on data using the supplied JSON Schema.
// schema is the schema object as a map[string]any (matching the way the
// template registry holds it after YAML decode); data is the populated
// attributes object. phase is "dispatch" or "commit" and is propagated
// into the returned error so the supervisor can route policy.
//
// Behaviour:
//   - Returns nil when data validates.
//   - Returns *ErrSchemaValidation when validation fails, when schema
//     marshalling fails, or when the schema itself fails to compile.
//
// The schema is compiled per-call. The supervisor invokes Validate at most
// twice per node-run (dispatch + commit), and the cost of compiling a
// small schema is negligible compared to the surrounding postgres + HTTP
// work. If profiling later shows compilation cost dominating, the
// supervisor can cache compiled schemas keyed by template id; this helper
// stays simple.
func Validate(schema map[string]any, data map[string]any, phase string) error {
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "marshal schema",
			Cause:   err,
		}
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("attributes-schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "add schema resource",
			Cause:   err,
		}
	}
	compiled, err := compiler.Compile("attributes-schema.json")
	if err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "compile schema",
			Cause:   err,
		}
	}
	// @deliberate: santhosh-tekuri's Validate accepts a Go value but
	// expects JSON-decoded shapes (map[string]any, []any, primitives).
	// Round-trip through json to normalise: a Go map produced by yaml.v3
	// may carry `map[any]any` subtrees that the validator rejects with a
	// confusing internal type-assertion error.
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "marshal data",
			Cause:   err,
		}
	}
	var normalised any
	if err := json.Unmarshal(dataBytes, &normalised); err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "unmarshal data",
			Cause:   err,
		}
	}
	if err := compiled.Validate(normalised); err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "data does not satisfy schema",
			Cause:   err,
		}
	}
	return nil
}
