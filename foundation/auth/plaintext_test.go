// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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
		"rk_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // wrong length suffix
	}
	for _, c := range cases {
		if err := ValidatePlaintext(c); !errors.Is(err, ErrInvalidPlaintext) {
			t.Errorf("ValidatePlaintext(%q): expected ErrInvalidPlaintext, got %v", c, err)
		}
	}
}
