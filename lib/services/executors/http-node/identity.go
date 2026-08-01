// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node/errorclasses"

const ExecutorName = "http-node"

const InProcURL = "inproc://http-node"

func SchemaBytes() []byte {
	return []byte(`{"type":"object"}`)
}

func DeclaredTags() []string {
	return []string{TagRateLimited}
}

const TagRateLimited = "rate_limited"

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
