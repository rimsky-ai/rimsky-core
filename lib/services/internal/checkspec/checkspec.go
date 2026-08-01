// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package checkspec

import (
	"fmt"
	"reflect"
)

func FieldList(cfg map[string]any) ([]string, error) {
	if v, ok := cfg["field"].(string); ok && v != "" {
		return []string{v}, nil
	}
	if vs, ok := cfg["fields"].([]any); ok {
		if len(vs) == 0 {
			return nil, fmt.Errorf("fields must be a non-empty array")
		}
		out := make([]string, 0, len(vs))
		for _, v := range vs {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("fields entry must be non-empty strings")
			}
			out = append(out, s)
		}
		return out, nil
	}
	if vs, ok := cfg["fields"].([]string); ok {
		if len(vs) == 0 {
			return nil, fmt.Errorf("fields must be a non-empty array")
		}
		return append([]string(nil), vs...), nil
	}
	return nil, fmt.Errorf("config: `field` (string) or `fields` ([]string) required")
}

func Numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	}
	return 0, false
}

func NumericDefault(v any, def float64) (float64, bool) {
	if x, ok := Numeric(v); ok {
		return x, true
	}
	return def, false
}
