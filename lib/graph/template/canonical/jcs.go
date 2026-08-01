// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func CanonicalBytes(m map[string]any) ([]byte, error) {
	if m == nil {
		m = map[string]any{}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal map: %w", err)
	}
	canon, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize map: %w", err)
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
