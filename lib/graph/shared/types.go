// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

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

func RenderResourcePath(segs []string) string {
	return strings.Join(segs, ":")
}
