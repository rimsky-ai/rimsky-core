// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKey_ActiveAt(t *testing.T) {
	t.Parallel()
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name string
		key  APIKey
		want bool
	}{
		{"no expiry no revoke", APIKey{}, true},
		{"revoked", APIKey{RevokedAt: &past}, false},
		{"expires_at in future", APIKey{ExpiresAt: &future}, true},
		{"expires_at in past", APIKey{ExpiresAt: &past}, false},
		{"revoke_at in future", APIKey{RevokeAt: &future}, true},
		{"revoke_at in past", APIKey{RevokeAt: &past}, false},
		{"revoked wins over future expiry", APIKey{RevokedAt: &past, ExpiresAt: &future}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, c.key.ActiveAt(now))
		})
	}
}
