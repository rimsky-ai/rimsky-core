// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	jsoncanonicalizer "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func CanonicalSpecBytes(spec node.TemplateSpec) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	canon, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize spec: %w", err)
	}
	return canon, nil
}

func CanonicalSpecHash(spec node.TemplateSpec) (string, error) {
	canon, err := CanonicalSpecBytes(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}
