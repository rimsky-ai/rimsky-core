// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node/errorclasses"

const ExecutorName = "http-node"

const InProcURL = "inproc://http-node"

// @decision: expected-attributes-schema-closed
func SchemaBytes() []byte {
	return []byte(`{"type":"object","properties":{"url":{"type":"string","default":""},"method":{"type":"string","default":"GET"},"headers":{"type":"object","additionalProperties":{"type":"string"},"default":{}},"body":{"default":null},"expect_status":{"type":"array","items":{"type":"integer"},"default":[]},"error_class_field":{"type":"string","default":""},"stub_probe":{"type":"boolean","default":false},"stub":{"type":"boolean","readOnly":true}},"additionalProperties":false}`)
}

func DeclaredTags() []string {
	return []string{TagRateLimited}
}

const TagRateLimited = "rate_limited"

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
