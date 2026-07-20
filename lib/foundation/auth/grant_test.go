// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrantRoundTrip(t *testing.T) {
	cases := []string{
		`{"action":"instance:read"}`,
		`{"action":"instance:create"}`,
		`{"action":"node:reset"}`,
	}
	for _, src := range cases {
		var e GrantEntry
		require.NoError(t, json.Unmarshal([]byte(src), &e), "unmarshal %s", src)
		out, err := json.Marshal(e)
		require.NoError(t, err, "marshal")
		var e2 GrantEntry
		require.NoError(t, json.Unmarshal(out, &e2), "re-unmarshal %s", out)
		require.Equal(t, e.Action, e2.Action, "round-trip differs")
	}
}

func TestGrantModeFirstClass(t *testing.T) {
	var e GrantEntry
	require.NoError(t, json.Unmarshal([]byte(`{"action":"instance:create","mode":"dry_run"}`), &e))
	require.Equal(t, "instance:create", e.Action)
	require.Equal(t, ModeDryRun, e.Mode)
	_, ok := e.Extras["mode"]
	require.False(t, ok, "mode should be lifted out of Extras: %+v", e.Extras)

	var bad GrantEntry
	err := json.Unmarshal([]byte(`{"action":"instance:create","mode":"sometimes"}`), &bad)
	require.ErrorIs(t, err, ErrInvalidGrant, "invalid mode")
}

func TestGrantScopeFirstClass(t *testing.T) {
	src := `{"action":"x","scope":{"template_tag":"y"},"rate_limit":"1/s"}`
	var e GrantEntry
	require.NoError(t, json.Unmarshal([]byte(src), &e))
	require.Equal(t, "y", e.Scope["template_tag"], "Scope = %+v", e.Scope)
	_, scopeInExtras := e.Extras["scope"]
	require.False(t, scopeInExtras, "scope should be lifted out of Extras: %+v", e.Extras)
	_, rateLimitInExtras := e.Extras["rate_limit"]
	require.True(t, rateLimitInExtras, "Extras missing genuinely-unknown 'rate_limit'")

	out, err := json.Marshal(e)
	require.NoError(t, err, "marshal")
	var roundTrip map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &roundTrip), "re-unmarshal")
	_, ok := roundTrip["scope"]
	require.True(t, ok, "re-marshaled output missing scope: %s", out)
	_, ok = roundTrip["rate_limit"]
	require.True(t, ok, "re-marshaled output missing rate_limit: %s", out)
}

func TestGrantModeScopeByteStableRoundTrip(t *testing.T) {
	e := GrantEntry{
		Action: "template:register",
		Mode:   ModeDryRun,
		Scope:  map[string]string{"zeta": "1", "alpha": "2"},
	}
	out, err := json.Marshal(e)
	require.NoError(t, err, "marshal")
	want := `{"action":"template:register","mode":"dry_run","scope":{"alpha":"2","zeta":"1"}}`
	require.Equal(t, want, string(out), "canonical form")
	var e2 GrantEntry
	require.NoError(t, json.Unmarshal(out, &e2), "re-unmarshal")
	out2, err := json.Marshal(e2)
	require.NoError(t, err, "re-marshal")
	require.Equal(t, string(out), string(out2), "not byte-stable")
}

func TestGrantInvalid(t *testing.T) {
	cases := []string{
		`{"action":""}`,
		`{}`,
	}
	for _, src := range cases {
		var e GrantEntry
		err := json.Unmarshal([]byte(src), &e)
		require.ErrorIs(t, err, ErrInvalidGrant, "unmarshal %s", src)
	}
}

func TestGrantArrayUnmarshal(t *testing.T) {
	src := `[{"action":"instance:read"},{"action":"node:*"}]`
	var g Grant
	require.NoError(t, json.Unmarshal([]byte(src), &g), "unmarshal grant")
	want := Grant{
		{Action: "instance:read"},
		{Action: "node:*"},
	}
	require.Equal(t, want, g, "grant array")
}
