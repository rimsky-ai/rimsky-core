// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestGrantRoundTrip(t *testing.T) {
	cases := []string{
		`{"action":"instance:read"}`,
		`{"action":"instance:create"}`,
		`{"action":"node:reset"}`,
	}
	for _, src := range cases {
		var e GrantEntry
		if err := json.Unmarshal([]byte(src), &e); err != nil {
			t.Fatalf("unmarshal %s: %v", src, err)
		}
		out, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var e2 GrantEntry
		if err := json.Unmarshal(out, &e2); err != nil {
			t.Fatalf("re-unmarshal %s: %v", out, err)
		}
		if e.Action != e2.Action {
			t.Errorf("round-trip differs: %+v vs %+v", e, e2)
		}
	}
}

func TestGrantModeFirstClass(t *testing.T) {
	var e GrantEntry
	if err := json.Unmarshal([]byte(`{"action":"instance:create","mode":"dry_run"}`), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Action != "instance:create" {
		t.Fatalf("action: %q", e.Action)
	}
	if e.Mode != ModeDryRun {
		t.Fatalf("Mode = %q, want %q", e.Mode, ModeDryRun)
	}
	if _, ok := e.Extras["mode"]; ok {
		t.Fatalf("mode should be lifted out of Extras: %+v", e.Extras)
	}

	var bad GrantEntry
	if err := json.Unmarshal([]byte(`{"action":"instance:create","mode":"sometimes"}`), &bad); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("invalid mode: expected ErrInvalidGrant, got %v", err)
	}
}

func TestGrantScopeFirstClass(t *testing.T) {
	src := `{"action":"x","scope":{"template_tag":"y"},"rate_limit":"1/s"}`
	var e GrantEntry
	if err := json.Unmarshal([]byte(src), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Scope["template_tag"] != "y" {
		t.Fatalf("Scope = %+v, want template_tag=y", e.Scope)
	}
	if _, ok := e.Extras["scope"]; ok {
		t.Fatalf("scope should be lifted out of Extras: %+v", e.Extras)
	}
	if _, ok := e.Extras["rate_limit"]; !ok {
		t.Fatalf("Extras missing genuinely-unknown 'rate_limit'")
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(out, &roundTrip); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if _, ok := roundTrip["scope"]; !ok {
		t.Fatalf("re-marshaled output missing scope: %s", out)
	}
	if _, ok := roundTrip["rate_limit"]; !ok {
		t.Fatalf("re-marshaled output missing rate_limit: %s", out)
	}
}

func TestGrantModeScopeByteStableRoundTrip(t *testing.T) {
	e := GrantEntry{
		Action: "template:register",
		Mode:   ModeDryRun,
		Scope:  map[string]string{"zeta": "1", "alpha": "2"},
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"action":"template:register","mode":"dry_run","scope":{"alpha":"2","zeta":"1"}}`
	if string(out) != want {
		t.Fatalf("canonical form:\n got %s\nwant %s", out, want)
	}
	var e2 GrantEntry
	if err := json.Unmarshal(out, &e2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	out2, err := json.Marshal(e2)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("not byte-stable: %s vs %s", out, out2)
	}
}

func TestGrantInvalid(t *testing.T) {
	cases := []string{
		`{"action":""}`,
		`{}`,
	}
	for _, src := range cases {
		var e GrantEntry
		err := json.Unmarshal([]byte(src), &e)
		if !errors.Is(err, ErrInvalidGrant) {
			t.Errorf("unmarshal %s: expected ErrInvalidGrant, got %v", src, err)
		}
	}
}

func TestGrantArrayUnmarshal(t *testing.T) {
	src := `[{"action":"instance:read"},{"action":"node:*"}]`
	var g Grant
	if err := json.Unmarshal([]byte(src), &g); err != nil {
		t.Fatalf("unmarshal grant: %v", err)
	}
	want := Grant{
		{Action: "instance:read"},
		{Action: "node:*"},
	}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("grant array: got %+v want %+v", g, want)
	}
}
