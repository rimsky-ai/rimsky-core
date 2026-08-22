// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
