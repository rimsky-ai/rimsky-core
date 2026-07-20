// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type ProducerVerb string

const (
	ProducerVerbCommit  ProducerVerb = "commit"
	ProducerVerbAbandon ProducerVerb = "abandon"
	ProducerVerbRelease ProducerVerb = "release"
)

type ProducerVerbOutboxRow struct {
	Seq                 int64
	ClaimHandleID       shared.UUID
	ProducerName        string
	Verb                ProducerVerb
	ClaimScopeData      []byte
	Address             []byte
	LeaseToken          string
	SupervisorID        string
	InstanceID          *shared.UUID
	ParentClaimHandleID *shared.UUID
	AttemptCount        int
	NextAttemptAt       time.Time
	LastError           string
	EnqueuedAt          time.Time
}

type ProducerVerbOutboxInsertInput struct {
	ClaimHandleID       shared.UUID
	ProducerName        string
	Verb                ProducerVerb
	ClaimScopeData      []byte
	Address             []byte
	LeaseToken          string
	SupervisorID        string
	InstanceID          *shared.UUID
	ParentClaimHandleID *shared.UUID
	NextAttemptAt       time.Time
	EnqueuedAt          time.Time
}

type ProducerVerbOutboxTable interface {
	Enqueue(ctx context.Context, in ProducerVerbOutboxInsertInput, tx Tx) error

	ListAll(ctx context.Context, tx Tx) ([]ProducerVerbOutboxRow, error)

	ListByProducer(ctx context.Context, producerName string, tx Tx) ([]ProducerVerbOutboxRow, error)

	RecordAttempt(ctx context.Context, seq int64, nextAttemptAt time.Time, lastError string, tx Tx) error

	Delete(ctx context.Context, seq int64, tx Tx) error

	CountByProducer(ctx context.Context, tx Tx) (map[string]int, error)
}
