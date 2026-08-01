// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func compileVerifierShapeChecksSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("verifier-shape-checks-schema.json", bytes.NewReader(SchemaBytes())); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("verifier-shape-checks-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateVerifierShapeChecksAttributes(t *testing.T, schema *jsonschema.Schema, doc string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	return schema.Validate(v)
}

func TestSchemaBytes_RejectsMissingChecks(t *testing.T) {
	schema := compileVerifierShapeChecksSchema(t)
	if err := validateVerifierShapeChecksAttributes(t, schema, `{}`); err == nil {
		t.Fatal("expected ExpectedAttributesSchema to reject a payload missing \"checks\" " +
			"(server.go hard-requires a non-empty attributes.checks array), got nil")
	}
}

func TestSchemaBytes_RejectsEmptyChecksArray(t *testing.T) {
	schema := compileVerifierShapeChecksSchema(t)
	if err := validateVerifierShapeChecksAttributes(t, schema, `{"checks":[]}`); err == nil {
		t.Fatal("expected ExpectedAttributesSchema to reject an empty \"checks\" array, got nil")
	}
}

func TestSchemaBytes_AcceptsNonEmptyChecksArray(t *testing.T) {
	schema := compileVerifierShapeChecksSchema(t)
	if err := validateVerifierShapeChecksAttributes(t, schema, `{"checks":[{"kind":"no_nulls"}]}`); err != nil {
		t.Fatalf("expected ExpectedAttributesSchema to accept a non-empty \"checks\" array, got %v", err)
	}
}
