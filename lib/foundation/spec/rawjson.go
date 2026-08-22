// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: rimsky-yml
package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const substitutionDirectiveOpen = "{{"

// @concept: rimsky-yml
// @concept: template
type RawJSON []byte

func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

func (r *RawJSON) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("spec.RawJSON: UnmarshalJSON on a nil pointer")
	}
	*r = append((*r)[0:0], data...)
	return nil
}

func (r RawJSON) MarshalYAML() (any, error) {
	if len(r) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(r, &value); err != nil {
		return nil, fmt.Errorf("spec.RawJSON: the stored value is not JSON: %w", err)
	}
	return value, nil
}

func (r *RawJSON) UnmarshalYAML(node *yaml.Node) error {
	if r == nil {
		return errors.New("spec.RawJSON: UnmarshalYAML on a nil pointer")
	}
	var value any
	if err := node.Decode(&value); err != nil {
		return err
	}
	if value == nil {
		*r = nil
		return nil
	}
	if text, isString := value.(string); isString {
		if trimmed := strings.TrimSpace(text); len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			if json.Valid([]byte(trimmed)) {
				*r = RawJSON(trimmed)
				return nil
			}
			if !strings.Contains(trimmed, substitutionDirectiveOpen) {
				return fmt.Errorf("spec.RawJSON: line %d: the value opens a JSON document and does not parse as JSON",
					node.Line)
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("spec.RawJSON: the YAML value does not encode as JSON: %w", err)
	}
	*r = encoded
	return nil
}
