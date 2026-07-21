// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifierhttp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func compileVerifierHTTPSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("verifier-http-schema.json", bytes.NewReader(SchemaBytes())); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("verifier-http-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateVerifierHTTPAttributes(t *testing.T, schema *jsonschema.Schema, doc string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	return schema.Validate(v)
}

func TestSchemaBytes_RejectsMissingURL(t *testing.T) {
	schema := compileVerifierHTTPSchema(t)
	if err := validateVerifierHTTPAttributes(t, schema, `{}`); err == nil {
		t.Fatal("expected ExpectedAttributesSchema to reject a payload missing \"url\" " +
			"(executor.go hard-requires attributes.url), got nil")
	}
}

func TestSchemaBytes_AcceptsURL(t *testing.T) {
	schema := compileVerifierHTTPSchema(t)
	if err := validateVerifierHTTPAttributes(t, schema, `{"url":"https://example.invalid/check"}`); err != nil {
		t.Fatalf("expected ExpectedAttributesSchema to accept a payload with \"url\", got %v", err)
	}
}
