// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for materialization-time tag substitution on the instance-
// factory path. Per spec

package controlapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstanceCreate_TagSubstitution(t *testing.T) {
	t.Run("static tag passes through", func(t *testing.T) {
		got, err := resolveNodeTags([]string{"setup", "recurring"}, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 2 || got[0] != "setup" || got[1] != "recurring" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("embedded mode with string param", func(t *testing.T) {
		params := json.RawMessage(`{"domain": "alpha.example.com"}`)
		got, err := resolveNodeTags([]string{"domain:{{params.domain}}"}, params)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got[0] != "domain:alpha.example.com" {
			t.Fatalf("got %q", got[0])
		}
	})

	t.Run("whole-directive lift with string param", func(t *testing.T) {
		params := json.RawMessage(`{"region": "us-west"}`)
		got, err := resolveNodeTags([]string{"{{params.region}}"}, params)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got[0] != "us-west" {
			t.Fatalf("got %q", got[0])
		}
	})

	t.Run("whole-directive lift with non-string param fails", func(t *testing.T) {
		params := json.RawMessage(`{"config": {"a": 1}}`)
		_, err := resolveNodeTags([]string{"{{params.config}}"}, params)
		if err == nil {
			t.Fatal("expected error for non-string lifted tag value")
		}
		if !strings.Contains(err.Error(), "non-string") {
			t.Fatalf("error should mention non-string: %v", err)
		}
	})

	t.Run("missing param fails", func(t *testing.T) {
		got, err := resolveNodeTags([]string{"{{params.missing}}"}, json.RawMessage(`{}`))
		if err == nil {
			t.Fatalf("expected error for missing param, got %v", got)
		}
		if !strings.Contains(err.Error(), "missing") && !strings.Contains(err.Error(), "param") {
			t.Fatalf("error should reference the missing source: %v", err)
		}
	})

	t.Run("embedded mode with numeric param stringifies", func(t *testing.T) {
		params := json.RawMessage(`{"version": 7}`)
		got, err := resolveNodeTags([]string{"v{{params.version}}"}, params)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got[0] != "v7" {
			t.Fatalf("got %q", got[0])
		}
	})
}
