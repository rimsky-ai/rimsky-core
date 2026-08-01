// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attribute_passthrough

func SchemaBytes() []byte {
	return []byte(`{
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "additionalProperties": true
}`)
}

func DeclaredTags() []string { return nil }

func DeclaredErrorClasses() []string { return nil }

const ExecutorAlias = "rimsky.attribute_passthrough"

const KindName = "attribute_passthrough"

const InProcURL = "inproc://attribute_passthrough"
