// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type BackoffKind = spec.BackoffKind

const (
	BackoffLinear      = spec.BackoffLinear
	BackoffExponential = spec.BackoffExponential
)
