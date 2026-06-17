// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package loop_counter ships the rimsky-bundled "loop_counter" utility
// node: an in-process executor handler that increments a count
// attribute across dispatches in a RunScope and tags its settling
// Success outcome with `loop` (while count < max) or `done` (at the
// cap). See `concept:executor` (in-process form) /
// `concept:terminal-tag` / `decision:loop-counter-shape`.
package loop_counter

// SchemaBytes returns the JSON Schema fragment for loop_counter's
// attributes. Advertised through the supervisor's
// ExpectedAttributesSchemaFor hook for the inproc loop_counter executor
// so registration-time validation works exactly as for out-of-process
// executors.
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

// DeclaredTags is the loop_counter handler's terminal-tag vocabulary.
//
// @concept: terminal-tag
func DeclaredTags() []string { return []string{"loop", "done"} }

// ExecutorAlias is the rimsky-side executor identity for the kind sugar.
const ExecutorAlias = "rimsky.loop_counter"

// KindName is the value template authors use as `kind: loop_counter`.
const KindName = "loop_counter"

// InProcURL is the executor.Endpoint.URL for loop_counter's inproc
// registration.
const InProcURL = "inproc://loop_counter"
