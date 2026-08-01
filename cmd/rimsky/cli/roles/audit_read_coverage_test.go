// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package roles

import (
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

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
		{"admin", true},
		{"read-only", true},
		{"operator", true},
		{"debug-operator", true},
		{"agent-supervisor", true},
		{"publisher-service", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.role, func(t *testing.T) {
			grant := roleGrant(t, c.role)
			got := auth.CheckGrant(grant, "audit:read", nil).Allowed
			if got != c.want {
				t.Fatalf("role %q audit:read coverage = %v, want %v (grant: %+v)",
					c.role, got, c.want, grant)
			}
		})
	}
}
