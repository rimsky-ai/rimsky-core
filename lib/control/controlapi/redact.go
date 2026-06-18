// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import "strings"

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
