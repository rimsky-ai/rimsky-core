// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pki

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	EnvCAEncryptionKey = "RIMSKY_CA_ENCRYPTION_KEY"

	caEncryptionKeyBytes = 32
)

var ErrCAKeyDecrypt = errors.New("pki: CA key decryption failed (wrong RIMSKY_CA_ENCRYPTION_KEY or corrupt ciphertext)")

func ParseCAEncryptionKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is not set", EnvCAEncryptionKey)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be standard base64: %w", EnvCAEncryptionKey, err)
	}
	if len(key) != caEncryptionKeyBytes {
		return nil, fmt.Errorf("%s must decode to exactly %d bytes, got %d", EnvCAEncryptionKey, caEncryptionKeyBytes, len(key))
	}
	return key, nil
}

// @decision: secret-at-rest-posture
func EncryptCAKey(plaintext, aesKey []byte) ([]byte, error) {
	gcm, err := newGCM(aesKey)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("pki.EncryptCAKey: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func DecryptCAKey(ciphertext, aesKey []byte) ([]byte, error) {
	gcm, err := newGCM(aesKey)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrCAKeyDecrypt
	}
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrCAKeyDecrypt
	}
	return plaintext, nil
}

func newGCM(aesKey []byte) (cipher.AEAD, error) {
	if len(aesKey) != caEncryptionKeyBytes {
		return nil, fmt.Errorf("pki: AES key must be %d bytes, got %d", caEncryptionKeyBytes, len(aesKey))
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("pki: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("pki: gcm: %w", err)
	}
	return gcm, nil
}
