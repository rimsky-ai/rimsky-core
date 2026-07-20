// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package loop_counter

func SchemaBytes() []byte {
	return []byte(`{
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "required": ["max"],
    "properties": {
        "max": { "type": "integer", "minimum": 1 },
        "count": { "type": "integer", "default": 0, "readOnly": true }
    },
    "additionalProperties": false
}`)
}

// @concept: terminal-tag
func DeclaredTags() []string { return []string{"loop", "done"} }

const AttributesSchemaFailedClass = "attributes_schema_failed"

func DeclaredErrorClasses() []string { return []string{AttributesSchemaFailedClass} }

const ExecutorAlias = "rimsky.loop_counter"

const KindName = "loop_counter"

const InProcURL = "inproc://loop_counter"
