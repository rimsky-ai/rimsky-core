// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	Prefix = "rk_"

	EntropyBytes = 33

	HashSize = sha256.Size
)

var ErrInvalidPlaintext = errors.New("auth: invalid api-key plaintext")

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

func Hash(plaintext string) [HashSize]byte {
	return sha256.Sum256([]byte(plaintext))
}

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
