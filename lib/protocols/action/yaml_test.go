// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package action

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUnmarshalAction(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		want   Action
		errSub string
	}{
		{"bare pop", "pop", Action{Kind: Pop}, ""},
		{"bare recycle", "recycle", Action{Kind: Recycle}, ""},
		{"bare pop_and_delete", "pop_and_delete", Action{Kind: PopAndDelete}, ""},
		{"pop_and_move with target", "{pop_and_move: guidance.failed}", Action{Kind: PopAndMove, MoveTarget: "guidance.failed"}, ""},
		{"pop_and_move target with quotes", "{pop_and_move: \"archive/done\"}", Action{Kind: PopAndMove, MoveTarget: "archive/done"}, ""},
		{"bare pop_and_move rejected", "pop_and_move", Action{}, "requires an inline target"},
		{"unknown action", "foo", Action{}, "unknown action"},
		{"old release_to_back", "release_to_back", Action{}, "unknown action"},
		{"old release_to_head", "release_to_head", Action{}, "unknown action"},
		{"old delete", "delete", Action{}, "unknown action"},
		{"empty map", "{}", Action{}, "empty action map"},
		{"multi-key map", "{pop_and_move: a, pop: b}", Action{}, "exactly one key"},
		{"sequence", "[pop]", Action{}, "must be a string or one-key map"},
		{"number", "42", Action{}, "must be a string or one-key map"},
		{"empty target", "{pop_and_move: \"\"}", Action{}, "must be non-empty"},
		{"non-parameterized in map shape", "{pop: x}", Action{}, "is not parameterized"},
		{"nested map value", "{pop_and_move: {nested: x}}", Action{}, "must be a string path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got Action
			err := yaml.Unmarshal([]byte(c.yaml), &got)
			if c.errSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != c.want {
					t.Fatalf("got %#v, want %#v", got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil (parsed %#v)", c.errSub, got)
			}
			if !strings.Contains(err.Error(), c.errSub) {
				t.Fatalf("error %q missing substring %q", err.Error(), c.errSub)
			}
		})
	}
}

func TestActionValidate(t *testing.T) {
	cases := []struct {
		name   string
		a      Action
		errSub string
	}{
		{"valid pop", Action{Kind: Pop}, ""},
		{"valid recycle", Action{Kind: Recycle}, ""},
		{"valid pop_and_move", Action{Kind: PopAndMove, MoveTarget: "x"}, ""},
		{"valid pop_and_delete", Action{Kind: PopAndDelete}, ""},
		{"unknown kind", Action{Kind: "x"}, "unknown action"},
		{"empty kind", Action{}, "unknown action"},
		{"pop_and_move missing target", Action{Kind: PopAndMove}, "requires a non-empty target"},
		{"non-parameterized with target", Action{Kind: Pop, MoveTarget: "x"}, "does not take a target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.Validate()
			if c.errSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.errSub)
			}
			if !strings.Contains(err.Error(), c.errSub) {
				t.Fatalf("error %q missing substring %q", err.Error(), c.errSub)
			}
		})
	}
}

func TestParseKind(t *testing.T) {
	for _, k := range AllKinds() {
		got, err := ParseKind(string(k))
		if err != nil {
			t.Errorf("ParseKind(%q): unexpected error %v", k, err)
			continue
		}
		if got != k {
			t.Errorf("ParseKind(%q) = %q, want %q", k, got, k)
		}
	}
	if _, err := ParseKind("nonsense"); err == nil {
		t.Errorf("ParseKind(nonsense): expected error, got nil")
	}
}
