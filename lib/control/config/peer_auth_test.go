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

// @story: peer-auth-mtls-mutual
// @decision: peer-auth-mtls
func TestPeerAuthMTLSImpliesRequiredTLSOnEveryPeerEntry(t *testing.T) {
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
peer_auth: mtls
claim_producers:
  store-a:
    endpoint: grpc://store-a:9000
    write_semantics_allowed: [sync]
executors:
  worker:
    transport: grpc
    endpoint: grpc://worker:9090
publishers:
  sensor:
    endpoint: grpc://sensor:9083
validators:
  shape:
    endpoint: grpc://shape:9095
    protocols: [validation, executor]
data_processors:
  dp:
    endpoint: grpc://dp:9099
`)
	for name, got := range map[string]string{
		"claim_producers[store-a]": cfg.ClaimProducers.ClaimProducers["store-a"].TLS,
		"executors[worker]":        cfg.Executors.Executors["worker"].TLS,
		"publishers[sensor]":       cfg.Publishers.Publishers["sensor"].TLS,
		"validators[shape]":        cfg.Validators.Validators["shape"].TLS,
		"data_processors[dp]":      cfg.DataProcessors.DataProcessors["dp"].TLS,
	} {
		if got != peer.TLSModeRequired {
			t.Errorf("%s tls = %q under peer_auth: mtls, want %q — the mode is one flip, not a flip plus a per-peer sweep",
				name, got, peer.TLSModeRequired)
		}
	}
}

// @decision: peer-auth-mtls
func TestPeerAuthMTLSHonoursAnExplicitPerPeerOverride(t *testing.T) {
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
peer_auth: mtls
executors:
  legacy:
    transport: grpc
    endpoint: grpc://legacy:9090
    tls: "off"
  hardened:
    transport: grpc
    endpoint: grpc://hardened:9090
`)
	if got := cfg.Executors.Executors["legacy"].TLS; got != peer.TLSModeOff {
		t.Errorf("an entry that explicitly writes tls: off must keep it, got %q", got)
	}
	if got := cfg.Executors.Executors["hardened"].TLS; got != peer.TLSModeRequired {
		t.Errorf("an entry that says nothing must inherit required, got %q", got)
	}
}

// @decision: peer-auth-mtls
func TestPeerAuthNoneLeavesPeerEntriesPlaintext(t *testing.T) {
	t.Setenv(pki.EnvCAEncryptionKey, "")
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
executors:
  worker:
    transport: grpc
    endpoint: grpc://worker:9090
`)
	if got := cfg.Executors.Executors["worker"].TLS; got != peer.TLSModeOff {
		t.Errorf("with peer_auth default none, an unset tls key stays %q, got %q — the default posture costs nothing",
			peer.TLSModeOff, got)
	}
}
