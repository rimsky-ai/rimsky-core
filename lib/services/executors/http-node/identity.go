// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node/errorclasses"

const ExecutorName = "http-node"

const InProcURL = "inproc://http-node"

func SchemaBytes() []byte {
	return []byte(`{"type":"object"}`)
}

func DeclaredTags() []string {
	return nil
}

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
