// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import "strings"

const RedactAllParamsSentinel = "*"

func ApplyParamsRedact(params map[string]any, redact []string) map[string]any {
	out := cloneParamsMap(params)
	for _, key := range redact {
		if key == RedactAllParamsSentinel {
			for k := range out {
				out[k] = "[REDACTED]"
			}
			return out
		}
		redactParamsPath(out, strings.Split(key, "."))
	}
	return out
}

func redactParamsPath(m map[string]any, path []string) {
	if m == nil || len(path) == 0 {
		return
	}
	head := path[0]
	if len(path) == 1 {
		if _, present := m[head]; present {
			m[head] = "[REDACTED]"
		}
		return
	}
	child, present := m[head]
	if !present {
		return
	}
	childMap, ok := child.(map[string]any)
	if !ok {
		return
	}
	clone := cloneParamsMap(childMap)
	redactParamsPath(clone, path[1:])
	m[head] = clone
}

func cloneParamsMap(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}
