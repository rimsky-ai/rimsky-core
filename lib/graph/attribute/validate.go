// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attribute

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

type ErrSchemaValidation struct {
	Phase   string
	Message string
	Cause   error
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

var compiledSchemaCache sync.Map

var schemaCompileCount atomic.Int64

func compiledSchema(schemaBytes []byte, phase string) (*jsonschema.Schema, error) {
	sum := sha256.Sum256(schemaBytes)
	key := hex.EncodeToString(sum[:])
	if cached, ok := compiledSchemaCache.Load(key); ok {
		return cached.(*jsonschema.Schema), nil
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("attributes-schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return nil, &ErrSchemaValidation{
			Phase:   phase,
			Message: "add schema resource",
			Cause:   err,
		}
	}
	compiled, err := compiler.Compile("attributes-schema.json")
	if err != nil {
		return nil, &ErrSchemaValidation{
			Phase:   phase,
			Message: "compile schema",
			Cause:   err,
		}
	}
	schemaCompileCount.Add(1)
	actual, _ := compiledSchemaCache.LoadOrStore(key, compiled)
	return actual.(*jsonschema.Schema), nil
}

func Validate(schema map[string]any, data map[string]any, phase string) error {
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return &ErrSchemaValidation{
			Phase:   phase,
			Message: "marshal schema",
			Cause:   err,
		}
	}
	compiled, err := compiledSchema(schemaBytes, phase)
	if err != nil {
		return err
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
			Cause:   ValueFreeValidationError(err),
		}
	}
	return nil
}

// @concept: inertness
// @concept: attribute
var valueBearingSchemaKeywords = map[string]bool{
	"const":            true,
	"enum":             true,
	"format":           true,
	"minimum":          true,
	"maximum":          true,
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"multipleOf":       true,
}

type schemaConstraintViolations struct {
	violations []string
}

func (e *schemaConstraintViolations) Error() string {
	return strings.Join(e.violations, "; ")
}

// @concept: inertness
func ValueFreeValidationError(err error) error {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	out := &schemaConstraintViolations{}
	collectValueFreeViolations(ve, &out.violations)
	if len(out.violations) == 0 {
		out.violations = []string{"the value does not satisfy the schema"}
	}
	return out
}

func collectValueFreeViolations(ve *jsonschema.ValidationError, into *[]string) {
	if len(ve.Causes) > 0 {
		for _, cause := range ve.Causes {
			collectValueFreeViolations(cause, into)
		}
		return
	}
	keyword := ve.KeywordLocation
	if idx := strings.LastIndex(keyword, "/"); idx >= 0 {
		keyword = keyword[idx+1:]
	}
	path := ve.InstanceLocation
	if path == "" {
		path = "/"
	}
	if keyword == "" {
		*into = append(*into, path+" does not satisfy the schema")
		return
	}
	if valueBearingSchemaKeywords[keyword] {
		*into = append(*into, path+" violates "+keyword)
		return
	}
	*into = append(*into, path+" violates "+keyword+": "+ve.Message)
}
