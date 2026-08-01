// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

type CliConfigError struct {
	Message string
}

func (e *CliConfigError) Error() string { return e.Message }

func (e *CliConfigError) ErrorClass() string { return "agent/attribute_invalid" }
