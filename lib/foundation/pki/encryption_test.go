// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package pki

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"testing"
)

func mustAESKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, caEncryptionKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("read key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := mustAESKey(t)
	plaintext := []byte("this is a marshaled CA private key")
	ciphertext, err := EncryptCAKey(plaintext, key)
	if err != nil {
		t.Fatalf("EncryptCAKey: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatalf("ciphertext must not contain plaintext")
	}
	got, err := DecryptCAKey(ciphertext, key)
	if err != nil {
		t.Fatalf("DecryptCAKey: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestDecryptWithWrongKeyReturnsError(t *testing.T) {
	key := mustAESKey(t)
	wrong := mustAESKey(t)
	ciphertext, err := EncryptCAKey([]byte("secret"), key)
	if err != nil {
		t.Fatalf("EncryptCAKey: %v", err)
	}
	got, err := DecryptCAKey(ciphertext, wrong)
	if !errors.Is(err, ErrCAKeyDecrypt) {
		t.Fatalf("wrong-key decrypt must return ErrCAKeyDecrypt, got err=%v", err)
	}
	if got != nil {
		t.Fatalf("wrong-key decrypt must return nil plaintext, got %q", got)
	}
}

func TestParseCAEncryptionKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(mustAESKey(t))
	if _, err := ParseCAEncryptionKey(valid); err != nil {
		t.Fatalf("valid 32-byte base64 key must parse: %v", err)
	}
	if _, err := ParseCAEncryptionKey(""); err == nil {
		t.Fatalf("empty key must fail")
	}
	if _, err := ParseCAEncryptionKey("not-base64!!!"); err == nil {
		t.Fatalf("non-base64 must fail")
	}
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, err := ParseCAEncryptionKey(short); err == nil {
		t.Fatalf("wrong-length key must fail")
	}
}
