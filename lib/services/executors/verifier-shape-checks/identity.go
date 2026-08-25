// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/errorclasses"

const ExecutorName = "verifier-shape-checks"

const InProcURL = "inproc://verifier-shape-checks"

// @decision: expected-attributes-schema-closed
func SchemaBytes() []byte {
	return []byte(`{"type":"object","properties":{"checks":{"type":"array","minItems":1},"rows":{"type":"array"},"verifier_pass":{"type":"boolean","readOnly":true},"verifier_checks":{"type":"integer","readOnly":true},"verifier_rows":{"type":"integer","readOnly":true},"verifier_warning_count":{"type":"integer","readOnly":true},"verifier_warnings":{"type":"array","readOnly":true},"stub_response":{"default":null},"stub_tags":{"default":null}},"required":["checks"],"additionalProperties":false}`)
}

func DeclaredTags() []string {
	return nil
}

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
