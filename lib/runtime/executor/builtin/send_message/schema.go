// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package send_message

import "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"

func SchemaBytes() []byte {
	return []byte(`{
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "additionalProperties": true
}`)
}

func DeclaredTags() []string { return nil }

func DeclaredErrorClasses() []string { return nil }

const ExecutorAlias = "rimsky.send_message"

const KindName = spec.SendMessageKindName

const InProcURL = "inproc://send_message"
