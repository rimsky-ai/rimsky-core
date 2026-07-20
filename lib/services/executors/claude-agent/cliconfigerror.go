// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

type CliConfigError struct {
	Message string
}

func (e *CliConfigError) Error() string { return e.Message }

func (e *CliConfigError) ErrorClass() string { return "agent/attribute_invalid" }
