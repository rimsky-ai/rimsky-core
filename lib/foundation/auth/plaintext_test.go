// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestMintDistinct(t *testing.T) {
	a, _, err := Mint()
	if err != nil {
		t.Fatalf("mint a: %v", err)
	}
	b, _, err := Mint()
	if err != nil {
		t.Fatalf("mint b: %v", err)
	}
	if a == b {
		t.Fatalf("Mint() produced duplicate plaintexts")
	}
	if !strings.HasPrefix(a, Prefix) {
		t.Fatalf("plaintext missing prefix: %q", a)
	}
}

// @blessed-invariant: plaintext-mint-once — exercised here: Mint produces a
// fresh API-key plaintext whose round-trip parse succeeds and whose prefix is
// the package's documented bound.
func TestMintPlaintextValidates(t *testing.T) {
	p, _, err := Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := ValidatePlaintext(p); err != nil {
		t.Fatalf("freshly-minted plaintext %q failed validate: %v", p, err)
	}
}

func TestMintHashMatches(t *testing.T) {
	p, h, err := Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got := Hash(p); got != h {
		t.Fatalf("Hash(plaintext) != hash from Mint")
	}
}

func TestValidatePlaintextRejects(t *testing.T) {
	cases := []string{
		"not-a-key",
		"",
		"rk_",
		"rk_short",
		"rk_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // @deliberate: wrong-length suffix triggers ErrInvalidPlaintext
	}
	for _, c := range cases {
		if err := ValidatePlaintext(c); !errors.Is(err, ErrInvalidPlaintext) {
			t.Errorf("ValidatePlaintext(%q): expected ErrInvalidPlaintext, got %v", c, err)
		}
	}
}
