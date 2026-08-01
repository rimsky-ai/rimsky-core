// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package roles

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func TestAgentSupervisorRoleRefusedDebugWriteVerbs(t *testing.T) {
	grant := roleGrant(t, "agent-supervisor")
	for _, action := range []string{
		"instance:pause",
		"instance:resume",
		"breakpoint:create",
		"breakpoint:resume",
		"breakpoint:delete",
		"instance:debug-override",
	} {
		action := action
		t.Run(action, func(t *testing.T) {
			got := auth.CheckGrant(grant, action, nil)
			if got.Allowed {
				t.Fatalf("agent-supervisor must not be granted %q until an operator explicitly "+
					"adds debug-operator; CheckGrant returned allowed (grant: %+v)", action, grant)
			}
		})
	}
}
