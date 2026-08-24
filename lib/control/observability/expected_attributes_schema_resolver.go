// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability

import "log/slog"

// @concept: attribute
func NewExpectedAttributesSchemaResolver(disc *Discovery) func(executorName string) (schema []byte, ok bool) {
	if disc == nil {
		return nil
	}
	return func(executorName string) ([]byte, bool) {
		entry, ok := disc.GetExecutor(executorName)
		if !ok || entry.Capabilities == nil {
			if !ok {
				slog.Debug("OBSERVABILITY.EXPECTEDATTRIBUTESSCHEMA.SKIPPED", "site", "expectedAttributesSchemaFor",
					"executor", executorName,
					"reason", "executor_not_in_capability_cache")
			} else {
				slog.Warn("OBSERVABILITY.EXPECTEDATTRIBUTESSCHEMA.SKIPPED", "site", "expectedAttributesSchemaFor",
					"executor", executorName,
					"reason", "executor_capabilities_nil")
			}
			return nil, false
		}
		schema := entry.Capabilities.ExpectedAttributesSchema
		if len(schema) == 0 {
			slog.Debug("OBSERVABILITY.EXPECTEDATTRIBUTESSCHEMA.ABSENT", "site", "expectedAttributesSchemaFor",
				"executor", executorName)
		}
		return schema, true
	}
}
