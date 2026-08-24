// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

const (
	ServiceAuthNone = enroll.ServiceAuthNone
	ServiceAuthMTLS = enroll.ServiceAuthMTLS
)

var ErrNoServerIdentity = errors.New("service: no server identity loaded")

type IdentityHolder struct {
	mu        sync.RWMutex
	cert      *tls.Certificate
	notBefore time.Time
	notAfter  time.Time
}

func NewIdentityHolder() *IdentityHolder { return &IdentityHolder{} }

func (h *IdentityHolder) Set(certPEM, keyPEM []byte, notBefore, notAfter time.Time) error {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("service.IdentityHolder.Set: load keypair: %w", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cert = &cert
	h.notBefore = notBefore
	h.notAfter = notAfter
	return nil
}

func (h *IdentityHolder) HasIdentity() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cert != nil
}

func (h *IdentityHolder) NotAfter() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.notAfter
}

func (h *IdentityHolder) GetClientCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.cert == nil {
		return &tls.Certificate{}, nil
	}
	return h.cert, nil
}

func (h *IdentityHolder) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.cert == nil {
		return nil, ErrNoServerIdentity
	}
	return h.cert, nil
}

func (h *IdentityHolder) NeedsRenewal(now time.Time) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.cert == nil {
		return true
	}
	return ShouldRenew(h.notBefore, h.notAfter, now)
}

func RenewalDeadline(notBefore, notAfter time.Time) time.Time {
	life := notAfter.Sub(notBefore)
	return notBefore.Add(life * 2 / 3)
}

func ShouldRenew(notBefore, notAfter, now time.Time) bool {
	return !now.Before(RenewalDeadline(notBefore, notAfter))
}

type EnrollFunc func(ctx context.Context) (certPEM, keyPEM []byte, notBefore, notAfter time.Time, err error)

func MaintainIdentity(ctx context.Context, h *IdentityHolder, enroll EnrollFunc, now func() time.Time, checkInterval time.Duration, onError func(error)) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !h.NeedsRenewal(now()) {
				continue
			}
			certPEM, keyPEM, notBefore, notAfter, err := enroll(ctx)
			if err != nil {
				if onError != nil {
					onError(err)
				}
				continue
			}
			if err := h.Set(certPEM, keyPEM, notBefore, notAfter); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
