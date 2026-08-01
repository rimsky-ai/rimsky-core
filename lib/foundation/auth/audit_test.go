// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
)

func TestKeyRevokedReasonEnumClosed(t *testing.T) {
	t.Parallel()
	closedSet := map[KeyRevokedReason]string{
		RevokeReasonManual:        `"manual"`,
		RevokeReasonRotationGrace: `"rotation_grace"`,
	}
	require.Len(t, closedSet, 2, "expected exactly two key_revoked reasons")
	for reason, wantJSON := range closedSet {
		data, err := json.Marshal(reason)
		require.NoError(t, err, "marshal %q", reason)
		require.Equal(t, wantJSON, string(data), "reason %q", reason)
	}
	_, expiredStillDefined := closedSet[KeyRevokedReason("expired")]
	require.False(t, expiredStillDefined, "'expired' must not be a member of the key_revoked reason enum")
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
		require.Equal(t, c.typed.String(), c.auditConst,
			"audit event constant must match its typed events.Kind wire form — the two must never drift apart")
		parsed, err := events.ParseKindString(c.auditConst)
		require.NoError(t, err, "events.ParseKindString(%q)", c.auditConst)
		require.Equal(t, c.typed, parsed, "round-trip must recover the same typed kind for %q", c.auditConst)
	}
}
