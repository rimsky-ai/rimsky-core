// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var DeploymentCASingletonID = shared.UUID{
	0xca, 0x00, 0x00, 0x01, 0x00, 0x00, 0x40, 0x00,
	0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
}

type DeploymentCA struct {
	ID             shared.UUID
	CACertPEM      []byte
	CAKeyEncrypted []byte
	CreatedAt      time.Time
}

type DeploymentCATable interface {
	Get(ctx context.Context, tx Tx) (DeploymentCA, bool, error)

	GetOrCreate(ctx context.Context, candidate DeploymentCA, tx Tx) (DeploymentCA, error)
}
