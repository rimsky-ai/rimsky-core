// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// @concept: rimsky-yml
func TestTemplateSpecReadsAMessageBodySchemaWrittenAsYAML(t *testing.T) {
	const doc = `name: yaml-body-schema
version: "1.0"
messages:
  - type: ping/recheck
    body_schema:
      type: object
      properties:
        reason:
          type: string
`
	var got TemplateSpec
	if err := yaml.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("a template author writes a message body schema as YAML, not as an embedded JSON string: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(got.Messages))
	}
	var schema map[string]any
	if err := json.Unmarshal(got.Messages[0].BodySchema, &schema); err != nil {
		t.Fatalf("the loaded body schema is not JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("body schema type = %v, want object", schema["type"])
	}
}

// @concept: rimsky-yml
func TestRawJSONKeepsAJSONStringAsWritten(t *testing.T) {
	var got MessageSchema
	if err := yaml.Unmarshal([]byte("type: ping\nbody_schema: '{\"type\":\"object\"}'\n"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.BodySchema) != `{"type":"object"}` {
		t.Fatalf("body_schema = %q, want the JSON string as written", got.BodySchema)
	}
}

// @concept: rimsky-yml
func TestRawJSONRoundTripsThroughYAMLAndJSON(t *testing.T) {
	original := MessageSchema{Type: "ping", BodySchema: RawJSON(`{"type":"object"}`)}

	viaJSON, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal to JSON: %v", err)
	}
	var fromJSON MessageSchema
	if err := json.Unmarshal(viaJSON, &fromJSON); err != nil {
		t.Fatalf("unmarshal from JSON: %v", err)
	}
	if string(fromJSON.BodySchema) != string(original.BodySchema) {
		t.Fatalf("JSON round trip gave %q, want %q", fromJSON.BodySchema, original.BodySchema)
	}

	viaYAML, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal to YAML: %v", err)
	}
	var fromYAML MessageSchema
	if err := yaml.Unmarshal(viaYAML, &fromYAML); err != nil {
		t.Fatalf("unmarshal from YAML: %v", err)
	}
	var want, got map[string]any
	if err := json.Unmarshal(original.BodySchema, &want); err != nil {
		t.Fatalf("decode the original: %v", err)
	}
	if err := json.Unmarshal(fromYAML.BodySchema, &got); err != nil {
		t.Fatalf("decode the YAML round trip: %v", err)
	}
	if got["type"] != want["type"] {
		t.Fatalf("YAML round trip gave %v, want %v", got, want)
	}
}

// @concept: rimsky-yml
func TestRawJSONOmitsAnAbsentValue(t *testing.T) {
	encoded, err := json.Marshal(MessageSchema{Type: "ping"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"type":"ping"}` {
		t.Fatalf("encoded = %s, want the absent body schema omitted", encoded)
	}
}

// @concept: rimsky-yml
// @concept: inertness
func TestRawJSONEncodesABareScalarAsJSON(t *testing.T) {
	var ref NodeClaimProducerRef
	if err := yaml.Unmarshal([]byte("name: p\nselector: s\nintent: rw\ndata: prod\n"), &ref); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(ref.Data) != `"prod"` {
		t.Fatalf("data = %q, want the scalar encoded as a JSON string; a bare scalar stored verbatim is not JSON",
			ref.Data)
	}
	if !json.Valid(ref.Data) {
		t.Fatalf("data = %q is not valid JSON", ref.Data)
	}
}

// @concept: rimsky-yml
// @concept: inertness
func TestRawJSONKeepsAQuotedScalarsType(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want string
	}{
		{"quoted number", "name: p\nselector: s\nintent: rw\ndata: \"123\"\n", `"123"`},
		{"quoted boolean", "name: p\nselector: s\nintent: rw\ndata: \"true\"\n", `"true"`},
		{"bare number", "name: p\nselector: s\nintent: rw\ndata: 123\n", `123`},
		{"bare boolean", "name: p\nselector: s\nintent: rw\ndata: true\n", `true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ref NodeClaimProducerRef
			if err := yaml.Unmarshal([]byte(tc.doc), &ref); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if string(ref.Data) != tc.want {
				t.Fatalf("data = %q, want %q; rimsky passes an opaque value through and never changes its type",
					ref.Data, tc.want)
			}
		})
	}
}

// @concept: rimsky-yml
func TestRawJSONRefusesAStringThatOpensAJSONDocumentAndDoesNotParse(t *testing.T) {
	var got MessageSchema
	err := yaml.Unmarshal([]byte("type: ping\nbody_schema: '{\"type\": '\n"), &got)
	if err == nil {
		t.Fatal("a string that opens a JSON document and does not parse must be refused, not stored")
	}
	if !strings.Contains(err.Error(), "does not parse as JSON") {
		t.Fatalf("error = %q, want it to say the value does not parse as JSON", err)
	}
}

// @concept: rimsky-yml
func TestRawJSONKeepsAWholeValueSubstitutionDirectiveAsAString(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want string
	}{
		{"publisher config", "name: p\nkind: k\nconfig: \"{{params.dsn}}\"\n", `"{{params.dsn}}"`},
		{"directive with a literal tail", "name: p\nkind: k\nconfig: \"{{params.host}}:5432\"\n", `"{{params.host}}:5432"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pub PublisherSpec
			if err := yaml.Unmarshal([]byte(tc.doc), &pub); err != nil {
				t.Fatalf("a whole-value substitution directive is a supported publisher config: %v", err)
			}
			if string(pub.Config) != tc.want {
				t.Fatalf("config = %q, want %q", pub.Config, tc.want)
			}
		})
	}
}

// @concept: rimsky-yml
func TestRawJSONKeepsAJSONDocumentThatCarriesADirectiveTraversable(t *testing.T) {
	var pub PublisherSpec
	const doc = "name: p\nkind: k\nconfig: '{\"dsn\": \"{{params.dsn}}\"}'\n"
	if err := yaml.Unmarshal([]byte(doc), &pub); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(pub.Config, &decoded); err != nil {
		t.Fatalf("a valid JSON document stays a JSON document so the substitution walk reaches its leaves: %v", err)
	}
	if decoded["dsn"] != "{{params.dsn}}" {
		t.Fatalf("dsn = %v, want the directive verbatim at the leaf", decoded["dsn"])
	}
}
