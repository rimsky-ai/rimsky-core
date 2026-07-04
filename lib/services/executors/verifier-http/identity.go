// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifierhttp

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http/errorclasses"

const ExecutorName = "verifier-http"

const InProcURL = "inproc://verifier-http"

func SchemaBytes() []byte {
	return []byte(`{"type":"object"}`)
}

func DeclaredTags() []string {
	return nil
}

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
