// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const identityRenewalCheckInterval = time.Minute

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
