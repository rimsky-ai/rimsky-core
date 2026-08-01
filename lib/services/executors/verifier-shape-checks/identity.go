// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/errorclasses"

const ExecutorName = "verifier-shape-checks"

const InProcURL = "inproc://verifier-shape-checks"

func SchemaBytes() []byte {
	return []byte(`{"type":"object","properties":{"checks":{"type":"array","minItems":1}},"required":["checks"]}`)
}

func DeclaredTags() []string {
	return nil
}

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
