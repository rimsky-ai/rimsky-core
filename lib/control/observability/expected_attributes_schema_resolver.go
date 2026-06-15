// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// expected_attributes_schema_resolver.go — schema-bytes lookup for the
// dispatch-time effective-schema computation. Replaces the role
// `userdata_validator.go` played pre-2026-05-21 (the validator was a
// schema check; the new resolver returns the schema bytes for the merge
// upstream of the check, which now happens inside
// `graph/attribute.Validate` against the merged effective attribute
// schema).

package observability

import "log/slog"

// NewExpectedAttributesSchemaResolver constructs a closure that, given
// an executor name, returns the executor's advertised
// expected_attributes_schema bytes from the discovery cache. ok=false
// when the discovery cache has no record for the named executor, or
// when its Capabilities advertise no schema.
//
// Wired into the runtime's `Config.ExpectedAttributesSchemaFor` field
// at supervisor construction. The runtime calls this closure at
// dispatch to compute the per-node effective attribute schema
// (executor schema ∪ L1 defaults ∪ L2 node declaration).
//
// nil discovery → nil closure (callers treat "no discovery wired" as
// "no executor schema in the merge" — typical of unit tests).
func NewExpectedAttributesSchemaResolver(disc *Discovery) func(executorName string) (schema []byte, ok bool) {
	if disc == nil {
		return nil
	}
	return func(executorName string) ([]byte, bool) {
		entry, ok := disc.GetExecutor(executorName)
		if !ok || entry.Capabilities == nil {
			if !ok {
				slog.Debug("expected_attributes_schema_resolver: skip",
					"executor", executorName,
					"reason", "executor_not_in_capability_cache")
			} else {
				slog.Warn("expected_attributes_schema_resolver: skip",
					"executor", executorName,
					"reason", "executor_capabilities_nil")
			}
			return nil, false
		}
		schema := entry.Capabilities.ExpectedAttributesSchema
		if len(schema) == 0 {
			slog.Debug("expected_attributes_schema_resolver: skip",
				"executor", executorName,
				"reason", "executor_advertised_no_schema")
			return nil, false
		}
		return schema, true
	}
}
