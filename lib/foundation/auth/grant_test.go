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
		`{"action":"node:invalidate"}`,
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
		// Round-trip back; verify Action preserved.
		var e2 GrantEntry
		if err := json.Unmarshal(out, &e2); err != nil {
			t.Fatalf("re-unmarshal %s: %v", out, err)
		}
		if e.Action != e2.Action {
			t.Errorf("round-trip differs: %+v vs %+v", e, e2)
		}
	}
}

// TestGrantLegacyModeFallsIntoExtras: a legacy `mode` key on a persisted
// grant is no longer a recognized field — it survives in Extras (no
// error) and is ignored by the matcher (pre-v1; no compat shim).
func TestGrantLegacyModeFallsIntoExtras(t *testing.T) {
	var e GrantEntry
	if err := json.Unmarshal([]byte(`{"action":"instance:create","mode":"dry_run"}`), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Action != "instance:create" {
		t.Fatalf("action: %q", e.Action)
	}
	if _, ok := e.Extras["mode"]; !ok {
		t.Fatalf("legacy mode key should land in Extras: %+v", e.Extras)
	}
}

func TestGrantExtrasRoundTrip(t *testing.T) {
	src := `{"action":"x","scope":{"template_tag":"y"},"rate_limit":"1/s"}`
	var e GrantEntry
	if err := json.Unmarshal([]byte(src), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := e.Extras["scope"]; !ok {
		t.Fatalf("Extras missing 'scope'")
	}
	if _, ok := e.Extras["rate_limit"]; !ok {
		t.Fatalf("Extras missing 'rate_limit'")
	}
	// Re-marshal; the extras should round-trip.
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
