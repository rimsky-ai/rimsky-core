// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// @decision: signoff-crypto-ed25519
const SignoffDomain = "rimsky/claude-agent/signoff/v1"

type RequiredSignoff struct {
	PublicKey string
	Path      string
}

type UnmetSignoff struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type SignoffResult struct {
	OK    bool
	Unmet []UnmetSignoff
}

func BuildSignoffMessage(nodeRunID string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	msg := SignoffDomain + "\n" + nodeRunID + "\n" + canonical
	return []byte(msg), nil
}

func canonicalizeJSON(raw []byte) (string, error) {
	wrapped := make([]byte, 0, len(raw)+2)
	wrapped = append(wrapped, '[')
	wrapped = append(wrapped, raw...)
	wrapped = append(wrapped, ']')
	canonical, err := jsoncanonicalizer.Transform(wrapped)
	if err != nil {
		return "", err
	}
	return string(canonical[1 : len(canonical)-1]), nil
}

func ValueAtPath(obj any, path string) any {
	if path == "" {
		return obj
	}
	cur := obj
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	return cur
}

func VerifyRequiredSignoffs(
	required []RequiredSignoff,
	attributesDelta map[string]any,
	nodeRunID string,
	signatures []string,
) SignoffResult {
	sigBufs := make([][]byte, 0, len(signatures))
	for _, s := range signatures {
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			raw = nil
		}
		sigBufs = append(sigBufs, raw)
	}

	unmet := []UnmetSignoff{}

	for _, req := range required {
		reportPath := req.Path
		if reportPath == "" {
			reportPath = "$"
		}
		if len(sigBufs) == 0 {
			unmet = append(unmet, UnmetSignoff{Path: reportPath, Reason: "missing"})
			continue
		}

		var value any
		if attributesDelta != nil {
			value = ValueAtPath(attributesDelta, req.Path)
		}
		message, err := BuildSignoffMessage(nodeRunID, value)
		if err != nil {
			unmet = append(unmet, UnmetSignoff{Path: reportPath, Reason: "invalid"})
			continue
		}

		key, err := parseEd25519PublicKeyPEM(req.PublicKey)
		if err != nil {
			unmet = append(unmet, UnmetSignoff{Path: reportPath, Reason: "invalid"})
			continue
		}

		satisfied := false
		for _, sig := range sigBufs {
			if len(sig) == ed25519.SignatureSize && ed25519.Verify(key, message, sig) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			unmet = append(unmet, UnmetSignoff{Path: reportPath, Reason: "invalid"})
		}
	}

	return SignoffResult{OK: len(unmet) == 0, Unmet: unmet}
}

func parseEd25519PublicKeyPEM(pemText string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errNotEd25519PEM
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errNotEd25519PEM
	}
	return key, nil
}

var errNotEd25519PEM = errors.New("public_key is not a PEM-encoded ed25519 SPKI key")
