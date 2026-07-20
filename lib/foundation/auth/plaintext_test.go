// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMintDistinct(t *testing.T) {
	a, _, err := Mint()
	require.NoError(t, err, "mint a")
	b, _, err := Mint()
	require.NoError(t, err, "mint b")
	require.NotEqual(t, a, b, "Mint() produced duplicate plaintexts")
	require.True(t, strings.HasPrefix(a, Prefix), "plaintext missing prefix: %q", a)
}

func TestMintPlaintextValidates(t *testing.T) {
	p, _, err := Mint()
	require.NoError(t, err, "mint")
	require.NoError(t, ValidatePlaintext(p), "freshly-minted plaintext %q failed validate", p)
}

func TestMintHashMatches(t *testing.T) {
	p, h, err := Mint()
	require.NoError(t, err, "mint")
	require.Equal(t, h, Hash(p), "Hash(plaintext) != hash from Mint")
}

func TestValidatePlaintextRejects(t *testing.T) {
	cases := []string{
		"not-a-key",
		"",
		"rk_",
		"rk_short",
		"rk_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, c := range cases {
		require.ErrorIs(t, ValidatePlaintext(c), ErrInvalidPlaintext, "ValidatePlaintext(%q)", c)
	}
}
