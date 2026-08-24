// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy
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
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
)

const localCASubject = "host-daemon-proxy"

// @decision: host-daemon-proxy-tls
type daemonFacingCredentials struct {
	Credentials credentials.TransportCredentials
	Source      string
	LocalCAPEM  []byte
}

// @concept: service-auth
// @decision: host-daemon-proxy-tls
func proxyServerCredentials(cfg Config, identity *serviceauth.Identity, now func() time.Time) (daemonFacingCredentials, error) {
	if cfg.Insecure {
		return daemonFacingCredentials{Source: "plaintext (" + envInsecureHop + ")"}, nil
	}
	if cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" {
		if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" {
			return daemonFacingCredentials{}, fmt.Errorf(
				"both RIMSKY_PROXY_TLS_CERT and RIMSKY_PROXY_TLS_KEY are required to serve the daemon-facing listener from a mounted keypair")
		}
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
		if err != nil {
			return daemonFacingCredentials{}, fmt.Errorf("load proxy TLS keypair: %w", err)
		}
		return daemonFacingCredentials{
			Credentials: credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
				ClientAuth:   tls.NoClientCert,
			}),
			Source: "operator-mounted keypair " + cfg.TLSCertPath,
		}, nil
	}
	if tlsCfg := identity.ServerOnlyTLSConfig(); tlsCfg != nil {
		return daemonFacingCredentials{Credentials: credentials.NewTLS(tlsCfg), Source: "enrolled deployment-CA leaf"}, nil
	}
	return locallyMintedDaemonFacingCredentials(now, cfg.LocalCAPath)
}

// @decision: host-daemon-proxy-tls
func locallyMintedDaemonFacingCredentials(now func() time.Time, caPath string) (daemonFacingCredentials, error) {
	holder, err := newLocalLeafHolder(now, caPath)
	if err != nil {
		return daemonFacingCredentials{}, err
	}
	return daemonFacingCredentials{
		Credentials: credentials.NewTLS(&tls.Config{
			GetCertificate: holder.getCertificate,
			MinVersion:     tls.VersionTLS12,
			ClientAuth:     tls.NoClientCert,
		}),
		Source:     "locally generated CA, published for the daemon to pin",
		LocalCAPEM: holder.ca.CertPEM(),
	}, nil
}

// @decision: host-daemon-proxy-tls
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
	if h.cert != nil && !serviceauth.ShouldRenew(h.notBefore, h.notAfter, at) {
		return h.cert, nil
	}
	issued, err := h.ca.IssueLeaf(localCASubject, at, pki.LeafTTL)
	if err != nil {
		return nil, fmt.Errorf("issue the proxy's local daemon-facing leaf: %w", err)
	}
	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load the proxy's local daemon-facing leaf: %w", err)
	}
	h.cert = &cert
	h.notBefore = issued.NotBefore
	h.notAfter = issued.NotAfter
	return h.cert, nil
}

// @decision: host-daemon-proxy-tls
func loadOrMintLocalCA(now func() time.Time, caPath string) (*pki.CA, error) {
	if caPath != "" {
		if ca, ok := loadPersistedLocalCA(caPath, now()); ok {
			slog.Info("PROXY.LOCALCA.REUSED",
				"path", caPath, "key_path", localCAKeyPath(caPath))
			return ca, nil
		}
	}
	ca, err := pki.GenerateCA(now())
	if err != nil {
		return nil, fmt.Errorf("generate the proxy's local daemon-facing CA: %w", err)
	}
	if caPath == "" {
		slog.Warn("PROXY.LOCALCA.EPHEMERAL",
			"detail", "the proxy generated a daemon-facing CA it cannot keep; name a file so a restart reuses the root every daemon pinned",
			"env", envLocalCAFile)
		return ca, nil
	}
	if err := persistLocalCA(caPath, ca); err != nil {
		return nil, err
	}
	return ca, nil
}

// @decision: host-daemon-proxy-tls
func loadPersistedLocalCA(caPath string, at time.Time) (*pki.CA, bool) {
	keyPath := localCAKeyPath(caPath)
	certPEM, certErr := os.ReadFile(caPath)
	keyDER, keyErr := os.ReadFile(keyPath)
	if errors.Is(certErr, fs.ErrNotExist) && errors.Is(keyErr, fs.ErrNotExist) {
		return nil, false
	}
	if certErr != nil || keyErr != nil {
		slog.Warn("PROXY.LOCALCA.UNREADABLE",
			"detail", "the proxy could not read the daemon-facing CA it published earlier and is generating a new one; every running daemon must re-pin the published root",
			"path", caPath, "key_path", keyPath, "cert_error", readErrorText(certErr), "key_error", readErrorText(keyErr))
		return nil, false
	}
	ca, err := pki.LoadCA(certPEM, keyDER, at)
	if err != nil {
		slog.Warn("PROXY.LOCALCA.UNLOADABLE",
			"detail", "the daemon-facing CA the proxy published earlier does not load, so the proxy is replacing it; every running daemon must re-pin the published root",
			"path", caPath, "key_path", keyPath, "error", err)
		return nil, false
	}
	return ca, true
}

// @decision: host-daemon-proxy-tls
func persistLocalCA(caPath string, ca *pki.CA) error {
	keyDER, err := ca.KeyPKCS8DER()
	if err != nil {
		return fmt.Errorf("encode the proxy's local daemon-facing CA key: %w", err)
	}
	if err := replaceFileAtomically(localCAKeyPath(caPath), keyDER, 0o600); err != nil {
		dropFile(localCAKeyPath(caPath))
		return fmt.Errorf("write the proxy's local daemon-facing CA key: %w", err)
	}
	if err := replaceFileAtomically(caPath, ca.CertPEM(), 0o644); err != nil {
		dropFile(caPath)
		dropFile(localCAKeyPath(caPath))
		return fmt.Errorf("write the proxy's local daemon-facing CA root: %w", err)
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
