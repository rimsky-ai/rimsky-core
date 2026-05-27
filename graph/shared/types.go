// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package shared

import (
	"strings"

	"github.com/rimsky-ai/rimsky-core/foundation/spec"
)

// Severity / BackoffKind / JitterKind are aliased from foundation/spec
// because they appear on persisted row types (policy-action backoff /
// jitter in TemplateSpec.Nodes[].ErrorTypes; the Severity enum is also
// re-used by service-side observability events). The canonical home is
// foundation/spec; this package re-exports for graph-layer call sites.

type Severity = spec.Severity

const (
	SeverityError   = spec.SeverityError
	SeverityWarning = spec.SeverityWarning
)

type BackoffKind = spec.BackoffKind

const (
	BackoffLinear      = spec.BackoffLinear
	BackoffExponential = spec.BackoffExponential
)

type JitterKind = spec.JitterKind

const (
	JitterNone      = spec.JitterNone
	JitterPlusMinus = spec.JitterPlusMinus
)

// AccessKind / MessageType / RenderResourcePath are graph-layer only —
// they do not appear on persisted rows and have no foundation/spec
// counterpart.

type AccessKind string

const (
	AccessInline AccessKind = "inline"
	AccessSQL    AccessKind = "sql"
	AccessMCP    AccessKind = "mcp"
	AccessREST   AccessKind = "rest"
)

type MessageType string

const (
	MessageInvalidate  MessageType = "invalidate"
	MessageRecalculate MessageType = "recalculate"
)

// RenderResourcePath renders segments as "a:b:c" for display.
func RenderResourcePath(segs []string) string {
	return strings.Join(segs, ":")
}
