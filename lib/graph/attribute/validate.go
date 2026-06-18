// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant 12 — Attributes validate twice.

package attributes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

type ErrSchemaValidation struct {
	Phase   string
	Message string
	Cause error
}

func (e *ErrSchemaValidation) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("attributes: schema validation failed (phase=%s): %s: %v", e.Phase, e.Message, e.Cause)
	}
	return fmt.Sprintf("attributes: schema validation failed (phase=%s): %s", e.Phase, e.Message)
}

func (e *ErrSchemaValidation) Unwrap() error { return e.Cause }

func IsSchemaValidation(err error) bool {
	var v *ErrSchemaValidation
	return errors.As(err, &v)
}

const (
	PhaseDispatch = "dispatch"
	PhaseCommit   = "commit"
)

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
