// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package matcher

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	refs := ValidationRefs{
		NodeTypes:     map[string]struct{}{"area-pass": {}, "deploy": {}},
		ExecutorNames: map[string]struct{}{"claude-agent": {}, "noop": {}},
		UsedExecutors: map[string]struct{}{"claude-agent": {}},
		GraphNames:    map[string]struct{}{"main": {}, "ingest": {}},
	}

	t.Run("empty matcher is valid", func(t *testing.T) {
		if err := Validate(Matcher{}, refs, 0); err != nil {
			t.Fatalf("empty matcher should validate; got %v", err)
		}
	})

	t.Run("unknown key rejected", func(t *testing.T) {
		err := Validate(Matcher{"bogus": "x"}, refs, 0)
		if err == nil {
			t.Fatal("unknown key should be rejected")
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("error must wrap ErrInvalid; got %v", err)
		}
	})

	t.Run("ordinal keys rejected", func(t *testing.T) {
		ordinals := []string{"dispatch_index", "nth_child", "partition_index", "seq"}
		for _, k := range ordinals {
			err := Validate(Matcher{k: 1}, refs, 0)
			if err == nil {
				t.Fatalf("ordinal key %q should be rejected", k)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ordinal key %q error must wrap ErrInvalid; got %v", k, err)
			}
			if !strings.Contains(err.Error(), "ordinal") {
				t.Fatalf("ordinal key %q error should mention 'ordinal'; got %v", k, err)
			}
		}
	})

	t.Run("child_key empty string rejected", func(t *testing.T) {
		err := Validate(Matcher{"child_key": ""}, refs, 0)
		if err == nil {
			t.Fatal("child_key=\"\" should be rejected")
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("error must wrap ErrInvalid; got %v", err)
		}
	})

	t.Run("child_key non-string rejected", func(t *testing.T) {
		err := Validate(Matcher{"child_key": 42}, refs, 0)
		if err == nil {
			t.Fatal("child_key=42 (non-string) should be rejected")
		}
	})

	t.Run("child_key non-empty string accepted", func(t *testing.T) {
		if err := Validate(Matcher{"child_key": "partition-a"}, refs, 0); err != nil {
			t.Fatalf("child_key=\"partition-a\" should validate; got %v", err)
		}
	})

	t.Run("node_type cross-check against NodeTypes", func(t *testing.T) {
		if err := Validate(Matcher{"node_type": "area-pass"}, refs, 0); err != nil {
			t.Fatalf("declared node_type should validate; got %v", err)
		}
		err := Validate(Matcher{"node_type": "unknown"}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("undeclared node_type should be rejected; got %v", err)
		}
	})

	t.Run("node_type cross-check skipped when NodeTypes nil", func(t *testing.T) {
		r := ValidationRefs{}
		if err := Validate(Matcher{"node_type": "anything"}, r, 0); err != nil {
			t.Fatalf("when NodeTypes is nil, any node_type should validate; got %v", err)
		}
	})

	t.Run("executor cross-check against ExecutorNames", func(t *testing.T) {
		if err := Validate(Matcher{"executor": "claude-agent"}, refs, 0); err != nil {
			t.Fatalf("declared+used executor should validate; got %v", err)
		}
		err := Validate(Matcher{"executor": "ghost"}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("undeclared executor should be rejected; got %v", err)
		}
	})

	t.Run("executor cross-check against UsedExecutors", func(t *testing.T) {
		err := Validate(Matcher{"executor": "noop"}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("declared-but-unused executor should be rejected; got %v", err)
		}
	})

	t.Run("executor UsedExecutors check skipped when nil (breakpoint mode)", func(t *testing.T) {
		r := ValidationRefs{
			ExecutorNames: map[string]struct{}{"claude-agent": {}, "noop": {}},
		}
		if err := Validate(Matcher{"executor": "noop"}, r, 0); err != nil {
			t.Fatalf("when UsedExecutors is nil, declared-but-unused executor should validate; got %v", err)
		}
	})

	t.Run("graph cross-check against GraphNames", func(t *testing.T) {
		if err := Validate(Matcher{"graph": "main"}, refs, 0); err != nil {
			t.Fatalf("main graph should validate; got %v", err)
		}
		if err := Validate(Matcher{"graph": "ingest"}, refs, 0); err != nil {
			t.Fatalf("declared sub-graph should validate; got %v", err)
		}
		err := Validate(Matcher{"graph": "ghost"}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("undeclared graph should be rejected; got %v", err)
		}
	})

	t.Run("LegacyFlat accepts only main", func(t *testing.T) {
		r := ValidationRefs{
			NodeTypes:     refs.NodeTypes,
			ExecutorNames: refs.ExecutorNames,
			UsedExecutors: refs.UsedExecutors,
			GraphNames:    map[string]struct{}{"main": {}},
			LegacyFlat:    true,
		}
		if err := Validate(Matcher{"graph": "main"}, r, 0); err != nil {
			t.Fatalf("LegacyFlat with graph=main should validate; got %v", err)
		}
		err := Validate(Matcher{"graph": "ingest"}, r, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("LegacyFlat with graph!=main should be rejected; got %v", err)
		}
	})

	t.Run("attrs primitive accepted", func(t *testing.T) {
		if err := Validate(Matcher{"attrs": map[string]any{"k": "v"}}, refs, 0); err != nil {
			t.Fatalf("string attr should validate; got %v", err)
		}
		if err := Validate(Matcher{"attrs": map[string]any{"k": true}}, refs, 0); err != nil {
			t.Fatalf("bool attr should validate; got %v", err)
		}
		if err := Validate(Matcher{"attrs": map[string]any{"k": float64(1)}}, refs, 0); err != nil {
			t.Fatalf("float64 attr should validate; got %v", err)
		}
	})

	t.Run("attrs non-primitive rejected", func(t *testing.T) {
		err := Validate(Matcher{"attrs": map[string]any{"k": []any{1, 2}}}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("array attr should be rejected; got %v", err)
		}
	})

	t.Run("attrs non-object rejected", func(t *testing.T) {
		err := Validate(Matcher{"attrs": "not-an-object"}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("attrs=string should be rejected; got %v", err)
		}
	})

	t.Run("entryIndex prefix shows when >= 0", func(t *testing.T) {
		err := Validate(Matcher{"bogus": "x"}, refs, 3)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "[3]") {
			t.Fatalf("error message should contain [3]; got %v", err)
		}
	})

	t.Run("entryIndex prefix suppressed when -1", func(t *testing.T) {
		err := Validate(Matcher{"bogus": "x"}, refs, -1)
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), "[-1]") {
			t.Fatalf("error message must not contain [-1]; got %v", err)
		}
		if strings.Contains(err.Error(), "matcher[") {
			t.Fatalf("error message must not contain a 'matcher[' prefix when entryIndex<0; got %v", err)
		}
	})

	t.Run("node_type non-string rejected", func(t *testing.T) {
		err := Validate(Matcher{"node_type": 1}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("non-string node_type should be rejected; got %v", err)
		}
	})

	t.Run("executor non-string rejected", func(t *testing.T) {
		err := Validate(Matcher{"executor": 1}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("non-string executor should be rejected; got %v", err)
		}
	})

	t.Run("graph non-string rejected", func(t *testing.T) {
		err := Validate(Matcher{"graph": 1}, refs, 0)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("non-string graph should be rejected; got %v", err)
		}
	})
}
