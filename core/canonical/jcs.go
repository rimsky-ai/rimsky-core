// Package canonical provides deterministic canonicalization and content
// hashing for template specs.
//
// RFC 8785 JSON Canonicalization Scheme (JCS) is used so that two
// semantically-identical TemplateSpec values — regardless of map ordering,
// whitespace, or non-essential string-escape variations — produce
// byte-identical canonical bytes and, in turn, identical hashes.
//
// @blessed-invariant: the canonical-hash function is the registry's identity
// function. Any change that alters output bytes for previously-registered
// specs is a breaking change. The JCS library version is pinned in go.mod.
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	jsoncanonicalizer "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"

	"github.com/fallguy/rimsky/core/node"
)

// CanonicalSpecHash returns the rimsky-side content hash of a TemplateSpec
// in the form "sha256-<64-hex>". The spec is JSON-marshalled via
// encoding/json, JCS-canonicalized, then SHA-256-hashed.
func CanonicalSpecHash(spec node.TemplateSpec) (string, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}
	canon, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalize spec: %w", err)
	}
	sum := sha256.Sum256(canon)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}
