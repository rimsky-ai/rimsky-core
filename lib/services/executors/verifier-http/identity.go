// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifierhttp

import "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http/errorclasses"

const ExecutorName = "verifier-http"

const InProcURL = "inproc://verifier-http"

func SchemaBytes() []byte {
	return []byte(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)
}

func DeclaredTags() []string {
	return nil
}

func DeclaredErrorClasses() []string {
	return errorclasses.Declared()
}
