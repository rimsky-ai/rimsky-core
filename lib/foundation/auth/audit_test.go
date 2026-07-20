// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"encoding/json"
	"testing"
)

func TestKeyRevokedReasonEnumClosed(t *testing.T) {
	t.Parallel()
	closedSet := map[KeyRevokedReason]string{
		RevokeReasonManual:        `"manual"`,
		RevokeReasonRotationGrace: `"rotation_grace"`,
	}
	if len(closedSet) != 2 {
		t.Fatalf("expected exactly two key_revoked reasons, got %d", len(closedSet))
	}
	for reason, wantJSON := range closedSet {
		data, err := json.Marshal(reason)
		if err != nil {
			t.Fatalf("marshal %q: %v", reason, err)
		}
		if string(data) != wantJSON {
			t.Fatalf("reason %q marshaled to %s, want %s", reason, data, wantJSON)
		}
	}
	if _, expiredStillDefined := closedSet[KeyRevokedReason("expired")]; expiredStillDefined {
		t.Fatalf("'expired' must not be a member of the key_revoked reason enum")
	}
}
