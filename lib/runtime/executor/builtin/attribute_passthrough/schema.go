// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attribute_passthrough

func SchemaBytes() []byte {
	return []byte(`{
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "additionalProperties": true
}`)
}

func DeclaredTags() []string { return nil }

const ExecutorAlias = "rimsky.attribute_passthrough"

const KindName = "attribute_passthrough"

const InProcURL = "inproc://attribute_passthrough"
