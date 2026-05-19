// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package auth provides the foundation-level types and helpers for
// rimsky's API-key Bearer-token auth. Pure functions and data types
// (plaintext format, grants, action wildcards, audit payload shapes,
// identity record); no I/O, no database imports. Shared by:
//
//   - control/controlapi (the auth middleware + endpoint handlers)
//   - cmd/rimsky (CLI bootstrap + role expansion)
//   - runtime (the rotation-grace sweep helper)
//
// Spec: .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md.
//
// @concept: api-key
// @concept: permission
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

const (
	// Prefix on every plaintext API key. Stable; do not change.
	Prefix = "rk_"

	// EntropyBytes is the size of the random suffix in bytes before
	// base64url encoding. 33 bytes → 44 base64url chars; total token
	// length is len(Prefix) + 44 = 47.
	EntropyBytes = 33

	// HashSize is the SHA-256 digest size (32 bytes).
	HashSize = sha256.Size
)

// ErrInvalidPlaintext is returned by ValidatePlaintext when the string
// is not a structurally well-formed rimsky API-key plaintext.
var ErrInvalidPlaintext = errors.New("auth: invalid api-key plaintext")

// Mint generates a fresh plaintext key. Returns the plaintext and its
// SHA-256 hash. The plaintext is the only artifact that ever leaves
// rimsky in a form the operator can re-present; the server retains
// only the hash.
//
// @blessed-invariant: plaintext-mint-once
func Mint() (plaintext string, hash [HashSize]byte, err error) {
	var buf [EntropyBytes]byte
	if _, err = rand.Read(buf[:]); err != nil {
		return "", [HashSize]byte{}, err
	}
	suffix := base64.RawURLEncoding.EncodeToString(buf[:])
	plaintext = Prefix + suffix
	hash = sha256.Sum256([]byte(plaintext))
	return plaintext, hash, nil
}

// Hash returns SHA-256(plaintext). Used at auth-middleware lookup time
// to compute the row-key without re-running CSPRNG.
func Hash(plaintext string) [HashSize]byte {
	return sha256.Sum256([]byte(plaintext))
}

// ValidatePlaintext returns nil if the string is a structurally
// well-formed rimsky API-key plaintext (prefix + correctly-sized
// base64url suffix). Used by the auth middleware to short-circuit
// obviously-malformed tokens without a DB lookup.
func ValidatePlaintext(s string) error {
	if !strings.HasPrefix(s, Prefix) {
		return ErrInvalidPlaintext
	}
	suffix := strings.TrimPrefix(s, Prefix)
	raw, err := base64.RawURLEncoding.DecodeString(suffix)
	if err != nil || len(raw) != EntropyBytes {
		return ErrInvalidPlaintext
	}
	return nil
}
