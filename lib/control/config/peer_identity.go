// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const identityRenewalCheckInterval = time.Minute

// @concept: peer-auth
type processIdentityHolder struct {
	mu       sync.Mutex
	holder   *peer.IdentityHolder
	ca       *pki.CA
	cancel   context.CancelFunc
	refCount int
}

var processIdentity processIdentityHolder

func (p *processIdentityHolder) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refCount--
	if p.refCount > 0 {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.holder = nil
	p.ca = nil
	p.cancel = nil
	p.refCount = 0
}

// @concept: peer-auth
func installPeerIdentity(ctx context.Context, tables persistence.Tables, principal string, clock shared.Clock, logger shared.Logger) (*peer.IdentityHolder, *pki.CA, func(), error) {
	if clock == nil {
		clock = shared.SystemClock{}
	}
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	var release = func() { processIdentity.release() }

	processIdentity.mu.Lock()
	defer processIdentity.mu.Unlock()
	if processIdentity.holder != nil {
		processIdentity.refCount++
		return processIdentity.holder, processIdentity.ca, release, nil
	}

	ca, err := ensureDeploymentCA(ctx, tables, clock)
	if err != nil {
		return nil, nil, nil, err
	}
	idCtx, cancel := context.WithCancel(context.Background())
	holder, err := setupOutboundIdentity(idCtx, ca, principal, clock, logger)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}

	processIdentity.holder = holder
	processIdentity.ca = ca
	processIdentity.cancel = cancel
	processIdentity.refCount = 1
	return holder, ca, release, nil
}

func setupOutboundIdentity(ctx context.Context, ca *pki.CA, principal string, clock shared.Clock, logger shared.Logger) (*peer.IdentityHolder, error) {
	holder := peer.NewIdentityHolder()
	enroll := func(context.Context) ([]byte, []byte, time.Time, time.Time, error) {
		issued, err := ca.IssueLeaf(principal, clock.Now(), pki.LeafTTL)
		if err != nil {
			return nil, nil, time.Time{}, time.Time{}, err
		}
		return issued.CertPEM, issued.KeyPEM, issued.NotBefore, issued.NotAfter, nil
	}
	certPEM, keyPEM, notBefore, notAfter, err := enroll(ctx)
	if err != nil {
		return nil, fmt.Errorf("peer identity for %q: %w", principal, err)
	}
	if err := holder.Set(certPEM, keyPEM, notBefore, notAfter); err != nil {
		return nil, fmt.Errorf("peer identity for %q: %w", principal, err)
	}
	peer.SetClientIdentity(holder)
	peer.SetTLSRootCAs(ca.CertPool())
	go peer.MaintainIdentity(ctx, holder, enroll, clock.Now, identityRenewalCheckInterval, func(err error) {
		logger.Warn("peer identity renewal failed", "principal", principal, "error", err.Error())
	})
	return holder, nil
}
