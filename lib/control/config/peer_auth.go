// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"fmt"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func ensureDeploymentCA(ctx context.Context, tables persistence.Tables, clock shared.Clock) (*pki.CA, error) {
	aesKey, err := pki.ParseCAEncryptionKey(os.Getenv(pki.EnvCAEncryptionKey))
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	existing, ok, err := tables.DeploymentCA().Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	if ok {
		return loadDeploymentCA(existing, aesKey)
	}
	ca, err := pki.GenerateCA(clock.Now())
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	keyDER, err := ca.KeyPKCS8DER()
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	encrypted, err := pki.EncryptCAKey(keyDER, aesKey)
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	candidate := persistence.DeploymentCA{
		CACertPEM:      ca.CertPEM(),
		CAKeyEncrypted: encrypted,
		CreatedAt:      clock.Now(),
	}
	winner, err := tables.DeploymentCA().GetOrCreate(ctx, candidate, nil)
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	return loadDeploymentCA(winner, aesKey)
}

func loadDeploymentCA(row persistence.DeploymentCA, aesKey []byte) (*pki.CA, error) {
	keyDER, err := pki.DecryptCAKey(row.CAKeyEncrypted, aesKey)
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	ca, err := pki.LoadCA(row.CACertPEM, keyDER)
	if err != nil {
		return nil, fmt.Errorf("deployment CA: %w", err)
	}
	return ca, nil
}
