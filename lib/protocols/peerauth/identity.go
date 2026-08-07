// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package peerauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

var ErrNoIdentity = errors.New("peerauth: no leaf identity loaded")

type enrollFunc func(ctx context.Context) (enroll.Response, error)

type Identity struct {
	mode   string
	now    func() time.Time
	enroll enrollFunc

	mu        sync.RWMutex
	cert      *tls.Certificate
	caPool    *x509.CertPool
	notBefore time.Time
	notAfter  time.Time
}

func (id *Identity) Enabled() bool { return id != nil && id.mode == enroll.PeerAuthMTLS }

func (id *Identity) refresh(ctx context.Context) error {
	resp, err := id.enroll(ctx)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair([]byte(resp.CertPEM), []byte(resp.KeyPEM))
	if err != nil {
		return fmt.Errorf("peerauth: load leaf keypair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(resp.CARootPEM)) {
		return fmt.Errorf("peerauth: enroll response ca_root_pem is not a valid certificate")
	}
	id.mu.Lock()
	defer id.mu.Unlock()
	id.cert = &cert
	id.caPool = pool
	id.notBefore = id.now()
	id.notAfter = resp.NotAfter
	return nil
}

func (id *Identity) getCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	id.mu.RLock()
	defer id.mu.RUnlock()
	if id.cert == nil {
		return nil, ErrNoIdentity
	}
	return id.cert, nil
}

func (id *Identity) getClientCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	id.mu.RLock()
	defer id.mu.RUnlock()
	if id.cert == nil {
		return &tls.Certificate{}, nil
	}
	return id.cert, nil
}

func (id *Identity) pool() *x509.CertPool {
	id.mu.RLock()
	defer id.mu.RUnlock()
	return id.caPool
}

func (id *Identity) ServerTLSConfig() *tls.Config {
	if !id.Enabled() {
		return nil
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		GetCertificate: id.getCertificate,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				MinVersion:     tls.VersionTLS12,
				ClientAuth:     tls.RequireAndVerifyClientCert,
				GetCertificate: id.getCertificate,
				ClientCAs:      id.pool(),
			}, nil
		},
	}
}

// @concept: peer-auth
func (id *Identity) ClientTLSConfig() *tls.Config {
	if !id.Enabled() {
		return nil
	}
	cfg := enroll.PinnedTLSConfig(id.pool())
	cfg.GetClientCertificate = id.getClientCertificate
	return cfg
}

func (id *Identity) GRPCServerOptions() []grpc.ServerOption {
	cfg := id.ServerTLSConfig()
	if cfg == nil {
		return nil
	}
	return []grpc.ServerOption{grpc.Creds(credentials.NewTLS(cfg))}
}

func (id *Identity) NeedsRenewal(now time.Time) bool {
	id.mu.RLock()
	defer id.mu.RUnlock()
	if id.cert == nil {
		return true
	}
	return ShouldRenew(id.notBefore, id.notAfter, now)
}

func (id *Identity) Maintain(ctx context.Context, checkInterval time.Duration, onError func(error)) {
	if !id.Enabled() {
		return
	}
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !id.NeedsRenewal(id.now()) {
				continue
			}
			if err := id.refresh(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

func RenewalDeadline(notBefore, notAfter time.Time) time.Time {
	life := notAfter.Sub(notBefore)
	return notBefore.Add(life * 2 / 3)
}

func ShouldRenew(notBefore, notAfter, now time.Time) bool {
	return !now.Before(RenewalDeadline(notBefore, notAfter))
}
