// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
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

func TestAuditEventKindsMatchTypedOperationalKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		auditConst string
		typed      events.Kind
	}{
		{EventAccessAttempted, events.KindAuthAccessAttempted()},
		{EventAccessDenied, events.KindAuthAccessDenied()},
		{EventKeyCreated, events.KindAuthKeyCreated()},
		{EventKeyRevoked, events.KindAuthKeyRevoked()},
		{EventKeyRotated, events.KindAuthKeyRotated()},
	}
	for _, c := range cases {
		if c.auditConst != c.typed.String() {
			t.Fatalf("audit event constant %q does not match its typed events.Kind wire form %q — the two must never drift apart", c.auditConst, c.typed.String())
		}
		parsed, err := events.ParseKindString(c.auditConst)
		if err != nil {
			t.Fatalf("events.ParseKindString(%q): %v", c.auditConst, err)
		}
		if parsed != c.typed {
			t.Fatalf("events.ParseKindString(%q) = %+v, want %+v (round-trip must recover the same typed kind)", c.auditConst, parsed, c.typed)
		}
	}
}
