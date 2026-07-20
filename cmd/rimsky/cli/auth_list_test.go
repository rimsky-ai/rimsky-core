// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/roles"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func TestGrantsEqual(t *testing.T) {
	a := auth.Grant{
		{Action: "instance:read"},
		{Action: "instance:write", Mode: auth.ModeExecute, Scope: map[string]string{"template": "t1"}},
	}
	t.Run("identical", func(t *testing.T) {
		b := auth.Grant{
			{Action: "instance:read"},
			{Action: "instance:write", Mode: auth.ModeExecute, Scope: map[string]string{"template": "t1"}},
		}
		if !grantsEqual(a, b) {
			t.Fatal("identical grants should be equal")
		}
	})
	t.Run("reordered is still equal", func(t *testing.T) {
		b := auth.Grant{
			{Action: "instance:write", Mode: auth.ModeExecute, Scope: map[string]string{"template": "t1"}},
			{Action: "instance:read"},
		}
		if !grantsEqual(a, b) {
			t.Fatal("reordered grants with the same entries should be equal")
		}
	})
	t.Run("differing mode is not equal", func(t *testing.T) {
		b := auth.Grant{
			{Action: "instance:read"},
			{Action: "instance:write", Mode: auth.ModeDryRun, Scope: map[string]string{"template": "t1"}},
		}
		if grantsEqual(a, b) {
			t.Fatal("grants differing only in Mode should not be equal")
		}
	})
	t.Run("differing scope is not equal", func(t *testing.T) {
		b := auth.Grant{
			{Action: "instance:read"},
			{Action: "instance:write", Mode: auth.ModeExecute, Scope: map[string]string{"template": "t2"}},
		}
		if grantsEqual(a, b) {
			t.Fatal("grants differing only in Scope should not be equal")
		}
	})
	t.Run("differing length is not equal", func(t *testing.T) {
		b := auth.Grant{{Action: "instance:read"}}
		if grantsEqual(a, b) {
			t.Fatal("grants of different length should not be equal")
		}
	})
}

func TestMatchRole(t *testing.T) {
	names := roles.AllNames()
	if len(names) == 0 {
		t.Fatal("expected at least one bundled role")
	}
	role, err := loadRole(names[0], "")
	if err != nil {
		t.Fatalf("loadRole(%q): %v", names[0], err)
	}
	if got := matchRole(role.Permissions); got != "role:"+names[0] {
		t.Fatalf("matchRole(bundled %q permissions) = %q, want %q", names[0], got, "role:"+names[0])
	}

	custom := auth.Grant{{Action: "definitely-not-a-bundled-permission-set"}}
	if got := matchRole(custom); got != "custom" {
		t.Fatalf("matchRole(custom grant) = %q, want %q", got, "custom")
	}
}

func TestParseExpiresDuration(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"hours", "24h", 24 * time.Hour, false},
		{"days", "30d", 30 * 24 * time.Hour, false},
		{"fractional_days", "1.5d", 36 * time.Hour, false},
		{"bare_d_is_not_a_day_count", "d", 0, true},
		{"garbage", "not-a-duration", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseExpiresDuration(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseExpiresDuration(%q) = %v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExpiresDuration(%q) returned error: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("parseExpiresDuration(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
