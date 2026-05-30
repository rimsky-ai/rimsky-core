// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Coverage check for the audit:read action across the bundled role
// templates (spec 2026-05-29-console-upstream-auth-audit-and-fixes).
// audit:read is granted separately from event:read because audit data
// (actor identity, IP, user-agent, actions) is sensitive. The blanket
// read roles cover it via wildcards; operator covers it via an explicit
// entry; a write-only service role must NOT cover it.
package roles

import (
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

// roleGrant loads a bundled role JSON by name and parses its
// permissions into an auth.Grant.
func roleGrant(t *testing.T, name string) auth.Grant {
	t.Helper()
	data, ok := Load(name)
	if !ok {
		t.Fatalf("role %q not found in bundle", name)
	}
	var doc struct {
		Permissions auth.Grant `json:"permissions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("role %q: unmarshal: %v", name, err)
	}
	return doc.Permissions
}

func TestRolesCoverAuditRead(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{"admin", true},              // "*" wildcard
		{"read-only", true},          // "*:read" wildcard
		{"operator", true},           // explicit { "action": "audit:read" }
		{"debug-operator", true},     // "*:read" wildcard
		{"agent-supervisor", true},   // "*:read" wildcard
		{"publisher-service", false}, // message:send only — must not cover audit
	}
	for _, c := range cases {
		c := c
		t.Run(c.role, func(t *testing.T) {
			grant := roleGrant(t, c.role)
			got := auth.CheckGrant(grant, "audit:read").Allowed
			if got != c.want {
				t.Fatalf("role %q audit:read coverage = %v, want %v (grant: %+v)",
					c.role, got, c.want, grant)
			}
		})
	}
}
