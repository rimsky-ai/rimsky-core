// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func seedTemplateInstanceAndMessage(t *testing.T, ctx context.Context, d persistence.Database) (uuid.UUID, uuid.UUID) {
	t.Helper()
	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	templateHash := "sha256-" + suffix
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_templates (id, spec, state)
        VALUES ($1, $2::jsonb, 'deployed')
    `, templateHash, `{}`)

	instanceID := uuid.New()
	mainScopeID := uuid.New()
	messageID := uuid.New()
	tables := d.Tables()
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		ck := "ck-" + instanceID.String()[:8]
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			InstanceKey:    &ck,
			MainRunScopeID: mainScopeID,
		}, tx); err != nil {
			return err
		}
		return tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         foundationshared.UUID(messageID),
			InstanceID: foundationshared.UUID(instanceID),
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("seed template+instance+message: %v", err)
	}
	return instanceID, messageID
}

func TestEnqueueFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)

	var got []uuid.UUID
	for i := 0; i < 3; i++ {
		err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			fid, err := frame.EnqueueFrame(ctx, d.Tables(), tx, instanceID, msgID)
			if err != nil {
				return err
			}
			got = append(got, fid)
			return nil
		})
		require.NoError(t, err)
	}
	require.Equal(t, 3, len(got))
	for i := range got {
		require.NotEqual(t, uuid.Nil, got[i])
		for j := i + 1; j < len(got); j++ {
			require.NotEqual(t, got[i], got[j], "EnqueueFrame must mint a fresh frame_id per call")
		}
	}

	var (
		count        int
		queuedCount  int
		matchTrigger int
	)
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT COUNT(*),
               COUNT(*) FILTER (WHERE state = 'queued'),
               COUNT(*) FILTER (WHERE triggering_message_id = $2)
        FROM rimsky_frames WHERE instance_id = $1
    `, []any{instanceID, msgID}, &count, &queuedCount, &matchTrigger)
	require.Equal(t, 3, count)
	require.Equal(t, 3, queuedCount)
	require.Equal(t, 3, matchTrigger)
}

func TestEnqueueFrame_InstanceNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, e := frame.EnqueueFrame(ctx, d.Tables(), tx, uuid.New(), uuid.New())
		return e
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
