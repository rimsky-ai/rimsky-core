// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dispatch-time userdata schema validator (plan F7). Mirrors the
// registration-time validator at modeling/node/template_validator.go::
// validateUserdataAgainstSchema, but runs after applyUserdataOverrides
// at dispatch time so per-instance overrides also pass through the
// executor's advertised schema. Returns a closure compatible with
// foundation/integration.Config.UserdataValidator and modeling/config
// .SupervisorConfig.UserdataValidator.

package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// NewUserdataValidator constructs a dispatch-time userdata-schema
// validator backed by the supplied Discovery cache. Returns nil when
// disc is nil so callers can treat "no discovery wired" as
// "validation disabled" (typical of unit tests).
//
// The closure performs a Discovery cache read on every dispatch and
// runs jsonschema.Validate against the advertised userdata_schema. The
// jsonschema compiler is recreated per invocation; for very high
// dispatch throughput a per-executor compiled-schema cache may be
// added later (the cache is keyed by schema bytes; cache invalidation
// follows the Discovery refresh).
//
// Fall-through behavior: when the Discovery cache has no entry for the
// executor (handshake hasn't landed) or the executor advertises no
// userdata schema, the validator accepts the userdata and logs at
// Debug — both cases are expected during normal operation (cold-start
// window before the first handshake, executors that don't ship a
// schema). The genuinely pathological case — executor present in the
// cache but Capabilities is nil — gets a Warn so operators notice it.
// The validator runs once per dispatch; under sustained load the Warn
// path would flood logs, hence the deliberate level split.
func NewUserdataValidator(disc *Discovery) func(executorName string, merged map[string]any) error {
	if disc == nil {
		return nil
	}
	return func(executorName string, merged map[string]any) error {
		entry, ok := disc.GetExecutor(executorName)
		if !ok || entry.Capabilities == nil {
			// Cache miss is expected at cold-start; a present-but-nil
			// capabilities entry is pathological (peer responded to
			// discovery but advertised nothing) and warrants a Warn.
			if !ok {
				slog.Debug("userdata-validator: skipping schema check",
					"executor", executorName,
					"reason", "executor_not_in_capability_cache")
			} else {
				slog.Warn("userdata-validator: skipping schema check",
					"executor", executorName,
					"reason", "executor_capabilities_nil")
			}
			return nil
		}
		schemaBytes := entry.Capabilities.UserdataSchema
		if len(schemaBytes) == 0 {
			// Executors without an advertised userdata schema are common
			// (typical on dev/test); demote to Debug to avoid log flood.
			slog.Debug("userdata-validator: skipping schema check",
				"executor", executorName,
				"reason", "executor_advertised_no_userdata_schema")
			return nil
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("inline://schema.json", bytes.NewReader(schemaBytes)); err != nil {
			return fmt.Errorf("executor %q advertised invalid userdata_schema: %w", executorName, err)
		}
		schema, err := compiler.Compile("inline://schema.json")
		if err != nil {
			return fmt.Errorf("executor %q userdata_schema does not compile: %w", executorName, err)
		}
		var doc any = map[string]any{}
		if len(merged) > 0 {
			udBytes, err := json.Marshal(merged)
			if err != nil {
				return fmt.Errorf("marshal merged userdata: %w", err)
			}
			if err := json.Unmarshal(udBytes, &doc); err != nil {
				return fmt.Errorf("decode merged userdata: %w", err)
			}
		}
		if err := schema.Validate(doc); err != nil {
			return fmt.Errorf("userdata fails executor %q schema: %w", executorName, err)
		}
		return nil
	}
}
