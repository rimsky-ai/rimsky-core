// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func TestParsePeerAuth(t *testing.T) {
	cases := map[string]string{
		"":     peer.PeerAuthNone,
		"none": peer.PeerAuthNone,
		"mtls": peer.PeerAuthMTLS,
	}
	for in, want := range cases {
		got, err := ParsePeerAuth(in)
		if err != nil || got != want {
			t.Fatalf("ParsePeerAuth(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParsePeerAuth("bogus"); err == nil {
		t.Fatalf("ParsePeerAuth(bogus) must error")
	}
}

func TestPeerAuthDefaultsToNoneWithoutCAKey(t *testing.T) {
	t.Setenv(pki.EnvCAEncryptionKey, "")
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
`)
	if cfg.PeerAuth != peer.PeerAuthNone {
		t.Fatalf("default PeerAuth = %q, want none", cfg.PeerAuth)
	}
}

func TestPeerAuthMTLSFailsClosedWithoutCAKey(t *testing.T) {
	t.Setenv(pki.EnvCAEncryptionKey, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
peer_auth: mtls
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadRimskyConfigYAML(path)
	if err == nil {
		t.Fatalf("peer_auth: mtls without %s must fail closed", pki.EnvCAEncryptionKey)
	}
	if !strings.Contains(err.Error(), pki.EnvCAEncryptionKey) {
		t.Fatalf("error %q should name %s", err.Error(), pki.EnvCAEncryptionKey)
	}
}

func TestPeerAuthMTLSAcceptedWithValidCAKey(t *testing.T) {
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
peer_auth: mtls
`)
	if cfg.PeerAuth != peer.PeerAuthMTLS {
		t.Fatalf("PeerAuth = %q, want mtls", cfg.PeerAuth)
	}
}
