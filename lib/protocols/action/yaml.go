// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package action

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func (a *Action) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			return fmt.Errorf("line %d: action must be a string or one-key map (got null)", node.Line)
		}
		if node.Tag != "" && node.Tag != "!!str" {
			return fmt.Errorf("line %d: action must be a string or one-key map (got %s)", node.Line, node.Tag)
		}
		kind, err := ParseKind(node.Value)
		if err != nil {
			return fmt.Errorf("line %d: %w", node.Line, err)
		}
		if kind == PopAndMove {
			return fmt.Errorf("line %d: action %q requires an inline target (use { %s: <target_path> })",
				node.Line, kind, kind)
		}
		a.Kind = kind
		return nil
	case yaml.MappingNode:
		if len(node.Content) == 0 {
			return fmt.Errorf("line %d: empty action map", node.Line)
		}
		if len(node.Content) != 2 {
			return fmt.Errorf("line %d: action map must have exactly one key (got %d)", node.Line, len(node.Content)/2)
		}
		keyNode := node.Content[0]
		valueNode := node.Content[1]
		if keyNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: action key must be a string", keyNode.Line)
		}
		kind, err := ParseKind(keyNode.Value)
		if err != nil {
			return fmt.Errorf("line %d: %w", keyNode.Line, err)
		}
		if kind != PopAndMove {
			return fmt.Errorf("line %d: action %q is not parameterized (use it as a bare string)", keyNode.Line, kind)
		}
		if valueNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: pop_and_move target must be a string path", valueNode.Line)
		}
		if valueNode.Value == "" {
			return fmt.Errorf("line %d: pop_and_move target must be non-empty", valueNode.Line)
		}
		a.Kind = PopAndMove
		a.MoveTarget = valueNode.Value
		return nil
	default:
		return fmt.Errorf("line %d: action must be a string or one-key map (got YAML kind %d)", node.Line, node.Kind)
	}
}
