// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy
package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
)

const localCASubject = "host-agent-proxy"

// @decision: host-agent-proxy-tls
type agentFacingCredentials struct {
	Credentials credentials.TransportCredentials
	Source      string
	LocalCAPEM  []byte
}

// @concept: peer-auth
// @decision: host-agent-proxy-tls
func proxyServerCredentials(cfg Config, identity *peerauth.Identity, now func() time.Time) (agentFacingCredentials, error) {
	if cfg.Insecure {
		return agentFacingCredentials{Source: "plaintext (" + envInsecureHop + ")"}, nil
	}
	if cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" {
		if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" {
			return agentFacingCredentials{}, fmt.Errorf(
				"both RIMSKY_PROXY_TLS_CERT and RIMSKY_PROXY_TLS_KEY are required to serve the agent-facing listener from a mounted keypair")
		}
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
		if err != nil {
			return agentFacingCredentials{}, fmt.Errorf("load proxy TLS keypair: %w", err)
		}
		return agentFacingCredentials{
			Credentials: credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
				ClientAuth:   tls.NoClientCert,
			}),
			Source: "operator-mounted keypair " + cfg.TLSCertPath,
		}, nil
	}
	if tlsCfg := identity.ServerOnlyTLSConfig(); tlsCfg != nil {
		return agentFacingCredentials{Credentials: credentials.NewTLS(tlsCfg), Source: "enrolled deployment-CA leaf"}, nil
	}
	return locallyMintedAgentFacingCredentials(now, cfg.LocalCAPath)
}

// @decision: host-agent-proxy-tls
func locallyMintedAgentFacingCredentials(now func() time.Time, caPath string) (agentFacingCredentials, error) {
	holder, err := newLocalLeafHolder(now, caPath)
	if err != nil {
		return agentFacingCredentials{}, err
	}
	return agentFacingCredentials{
		Credentials: credentials.NewTLS(&tls.Config{
			GetCertificate: holder.getCertificate,
			MinVersion:     tls.VersionTLS12,
			ClientAuth:     tls.NoClientCert,
		}),
		Source:     "locally generated CA, published for the agent to pin",
		LocalCAPEM: holder.ca.CertPEM(),
	}, nil
}

// @decision: host-agent-proxy-tls
type localLeafHolder struct {
	ca  *pki.CA
	now func() time.Time

	mu        sync.Mutex
	cert      *tls.Certificate
	notBefore time.Time
	notAfter  time.Time
}

func newLocalLeafHolder(now func() time.Time, caPath string) (*localLeafHolder, error) {
	ca, err := loadOrMintLocalCA(now, caPath)
	if err != nil {
		return nil, err
	}
	holder := &localLeafHolder{ca: ca, now: now}
	if _, err := holder.currentLeaf(); err != nil {
		return nil, err
	}
	return holder, nil
}

func (h *localLeafHolder) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return h.currentLeaf()
}

func (h *localLeafHolder) currentLeaf() (*tls.Certificate, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	at := h.now()
	if h.cert != nil && !peerauth.ShouldRenew(h.notBefore, h.notAfter, at) {
		return h.cert, nil
	}
	issued, err := h.ca.IssueLeaf(localCASubject, at, pki.LeafTTL)
	if err != nil {
		return nil, fmt.Errorf("issue the proxy's local agent-facing leaf: %w", err)
	}
	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load the proxy's local agent-facing leaf: %w", err)
	}
	h.cert = &cert
	h.notBefore = issued.NotBefore
	h.notAfter = issued.NotAfter
	return h.cert, nil
}

// @decision: host-agent-proxy-tls
func loadOrMintLocalCA(now func() time.Time, caPath string) (*pki.CA, error) {
	if caPath != "" {
		if ca, ok := loadPersistedLocalCA(caPath, now()); ok {
			slog.Info("the proxy reused the agent-facing CA it published on an earlier run",
				"path", caPath, "key_path", localCAKeyPath(caPath))
			return ca, nil
		}
	}
	ca, err := pki.GenerateCA(now())
	if err != nil {
		return nil, fmt.Errorf("generate the proxy's local agent-facing CA: %w", err)
	}
	if caPath == "" {
		slog.Warn("the proxy generated an agent-facing CA it cannot keep; name a file so a restart reuses the "+
			"root every agent pinned", "env", envLocalCAFile)
		return ca, nil
	}
	if err := persistLocalCA(caPath, ca); err != nil {
		return nil, err
	}
	return ca, nil
}

// @decision: host-agent-proxy-tls
func loadPersistedLocalCA(caPath string, at time.Time) (*pki.CA, bool) {
	keyPath := localCAKeyPath(caPath)
	certPEM, certErr := os.ReadFile(caPath)
	keyDER, keyErr := os.ReadFile(keyPath)
	if errors.Is(certErr, fs.ErrNotExist) && errors.Is(keyErr, fs.ErrNotExist) {
		return nil, false
	}
	if certErr != nil || keyErr != nil {
		slog.Warn("the proxy could not read the agent-facing CA it published earlier and is generating a new one; "+
			"every running agent must re-pin the published root",
			"path", caPath, "key_path", keyPath, "cert_error", readErrorText(certErr), "key_error", readErrorText(keyErr))
		return nil, false
	}
	ca, err := pki.LoadCA(certPEM, keyDER, at)
	if err != nil {
		slog.Warn("the agent-facing CA the proxy published earlier does not load, so the proxy is replacing it; "+
			"every running agent must re-pin the published root",
			"path", caPath, "key_path", keyPath, "error", err)
		return nil, false
	}
	return ca, true
}

// @decision: host-agent-proxy-tls
func persistLocalCA(caPath string, ca *pki.CA) error {
	keyDER, err := ca.KeyPKCS8DER()
	if err != nil {
		return fmt.Errorf("encode the proxy's local agent-facing CA key: %w", err)
	}
	if err := replaceFileAtomically(localCAKeyPath(caPath), keyDER, 0o600); err != nil {
		dropFile(localCAKeyPath(caPath))
		return fmt.Errorf("write the proxy's local agent-facing CA key: %w", err)
	}
	if err := replaceFileAtomically(caPath, ca.CertPEM(), 0o644); err != nil {
		dropFile(caPath)
		dropFile(localCAKeyPath(caPath))
		return fmt.Errorf("write the proxy's local agent-facing CA root: %w", err)
	}
	return nil
}

func localCAKeyPath(caPath string) string { return caPath + ".key" }

func readErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
