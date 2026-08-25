// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifierhttp

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http/errorclasses"

const ExecutorName = "verifier-http"

const InProcURL = "inproc://verifier-http"

// @decision: expected-attributes-schema-closed
func SchemaBytes() []byte {
	return []byte(`{"type":"object","properties":{"url":{"type":"string"},"body":{"default":null},"timeout_ms":{"type":"integer","minimum":1,"default":60000},"expected_status":{"type":"array","items":{"type":"integer"},"default":[]},"class_field":{"type":"string","default":"class"},"verifier_pass":{"type":"boolean","readOnly":true},"verifier_status":{"type":"integer","readOnly":true},"stub_response":{"default":null},"stub_tags":{"default":null}},"required":["url"],"additionalProperties":false}`)
}

func DeclaredTags() []string {
	return nil
}

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
