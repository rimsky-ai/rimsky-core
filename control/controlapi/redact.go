// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import "strings"

// ApplyParamsRedact returns a copy of params with each top-level key listed
// in redact replaced by the sentinel "[REDACTED]". Shallow only (nested
// object keys are not walked) — matches spec §5.5 (params_redact on
// TemplateSpec).
//
// If redact is empty, returns the original map (not a copy). If any key
// contains a dot, logs a note (not an error) — dotted keys aren't supported
// in v1.
func ApplyParamsRedact(params map[string]any, redact []string) map[string]any {
	if len(redact) == 0 {
		return params
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	for _, key := range redact {
		if strings.Contains(key, ".") {
			continue
		}
		if _, present := out[key]; present {
			out[key] = "[REDACTED]"
		}
	}
	return out
}
