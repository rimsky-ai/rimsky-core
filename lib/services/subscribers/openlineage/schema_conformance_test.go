// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const openLineageRunEventSchemaJSON = `{
	"$schema": "http://json-schema.org/draft-07/schema#",
	"type": "object",
	"properties": {
		"eventType": {"type": "string", "enum": ["START", "RUNNING", "COMPLETE", "ABORT", "FAIL", "OTHER"]},
		"eventTime": {"type": "string", "format": "date-time"},
		"producer": {"type": "string", "format": "uri"},
		"schemaURL": {"type": "string", "format": "uri"},
		"run": {
			"type": "object",
			"properties": {
				"runId": {"type": "string", "format": "uuid"},
				"facets": {"type": "object"}
			},
			"required": ["runId"]
		},
		"job": {
			"type": "object",
			"properties": {
				"namespace": {"type": "string", "minLength": 1},
				"name": {"type": "string", "minLength": 1},
				"facets": {"type": "object"}
			},
			"required": ["namespace", "name"]
		},
		"inputs": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"namespace": {"type": "string"},
					"name": {"type": "string"}
				},
				"required": ["namespace", "name"]
			}
		},
		"outputs": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"namespace": {"type": "string"},
					"name": {"type": "string"}
				},
				"required": ["namespace", "name"]
			}
		}
	},
	"required": ["eventType", "eventTime", "run", "job", "producer", "schemaURL"]
}`

func compileOpenLineageRunEventSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("openlineage-runevent.json", bytes.NewReader([]byte(openLineageRunEventSchemaJSON))); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("openlineage-runevent.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateAgainstOpenLineageSchema(t *testing.T, schema *jsonschema.Schema, ev Event) error {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return schema.Validate(v)
}

func TestMakeLeafRunEvent_ConformsToOpenLineageRunEventSchema(t *testing.T) {
	schema := compileOpenLineageRunEventSchema(t)
	rec := LeafRunRecord{
		RunID:              "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		NodeID:             "6ba7b811-9dad-11d1-80b4-00c04fd430c8",
		FrameID:            "6ba7b812-9dad-11d1-80b4-00c04fd430c8",
		NodeAlias:          "draft",
		TemplateNodeAlias:  "draft",
		TemplateHash:       "sha256-aaa",
		ExecutorName:       "claude-agent",
		Changed:            true,
		SettlingSignalType: "terminal/success",
		TerminalKind:       "complete",
		HeldClaims: []HeldClaimRef{
			{ClaimHandleID: "6ba7b813-9dad-11d1-80b4-00c04fd430c8", Role: "acquire", ProducerName: "topics-ring", ScopeDataHash: "scope-hash-1"},
		},
	}
	ev := MakeLeafRunEvent(rec, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC), "analytics")
	if err := validateAgainstOpenLineageSchema(t, schema, ev); err != nil {
		t.Fatalf("MakeLeafRunEvent output does not conform to the OpenLineage RunEvent schema: %v\nevent: %+v", err, ev)
	}
}

func TestMakeClaimTerminalEvent_ConformsToOpenLineageRunEventSchema(t *testing.T) {
	schema := compileOpenLineageRunEventSchema(t)
	cases := []struct {
		name string
		rec  ClaimTerminalRecord
	}{
		{
			name: "committed, falls back to claim_handle_id as runId",
			rec: ClaimTerminalRecord{
				ClaimHandleID: "6ba7b814-9dad-11d1-80b4-00c04fd430c8",
				RunID:         "6ba7b815-9dad-11d1-80b4-00c04fd430c8",
				NodeID:        "6ba7b816-9dad-11d1-80b4-00c04fd430c8",
				FrameID:       "6ba7b817-9dad-11d1-80b4-00c04fd430c8",
				ProducerName:  "parquet-store",
				ScopeDataHash: "scope-hash-2",
				Outcome:       "committed",
			},
		},
		{
			name: "abandoned, explicit OpenLineageRunRef",
			rec: ClaimTerminalRecord{
				ClaimHandleID:     "6ba7b818-9dad-11d1-80b4-00c04fd430c8",
				RunID:             "6ba7b819-9dad-11d1-80b4-00c04fd430c8",
				NodeID:            "6ba7b81a-9dad-11d1-80b4-00c04fd430c8",
				FrameID:           "6ba7b81b-9dad-11d1-80b4-00c04fd430c8",
				OpenLineageRunRef: "6ba7b81c-9dad-11d1-80b4-00c04fd430c8",
				ProducerName:      "parquet-store",
				ScopeDataHash:     "scope-hash-3",
				Outcome:           "abandoned",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := MakeClaimTerminalEvent(tc.rec, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC), "analytics")
			if err := validateAgainstOpenLineageSchema(t, schema, ev); err != nil {
				t.Fatalf("MakeClaimTerminalEvent output does not conform to the OpenLineage RunEvent schema: %v\nevent: %+v", err, ev)
			}
		})
	}
}

func TestMakeClaimTerminalEvent_NonUUIDClaimHandleIDFailsSchemaWithoutOpenLineageRunRef(t *testing.T) {
	schema := compileOpenLineageRunEventSchema(t)
	rec := ClaimTerminalRecord{
		ClaimHandleID: "not-a-uuid",
		ProducerName:  "parquet-store",
		ScopeDataHash: "scope-hash-4",
		Outcome:       "committed",
	}
	ev := MakeClaimTerminalEvent(rec, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC), "analytics")
	if err := validateAgainstOpenLineageSchema(t, schema, ev); err == nil {
		t.Fatal("expected schema validation to reject a non-UUID run.runId " +
			"(claim_handle_id is a shared.UUID in production; this pins that OpenLineageRunRef " +
			"or a UUID-shaped claim_handle_id is load-bearing for run.runId conformance), got nil")
	}
}
