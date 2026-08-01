// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestSweepDeliverMessages_PayloadNeverLoggedOnlySubstituted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "payload-inertness", Version: "1",
	})
	ck := "ck-payload-inertness"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var receiverNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "secret-carrier", Executor: "",
		}, tx)
		if err != nil {
			return err
		}
		receiverNode = n
		return nil
	}))

	const secretMarker = "sk-do-not-log-me-e9f3a1"
	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: inst.ID,
			Type:       "secret-carrier",
			Payload:    []byte(fmt.Sprintf(`{"secret":%q}`, secretMarker)),
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, inst.ID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))

	capLogger := shared.NewCapturingLogger()
	require.NoError(t, runtime.SweepDeliverTriggeringMessagesForRunningFrames(ctx, backend, capLogger, time.Now()))

	var latest *persistence.NodeRunLatest
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		l, err := backend.Nodes().GetLatestRunForNode(ctx, receiverNode.ID, tx)
		latest = l
		return err
	}))
	require.NotNil(t, latest)
	require.Equal(t, frameID, latest.FrameID)

	var attrs *persistence.NodeAttributesRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		a, err := backend.NodeAttributes().GetByRun(ctx, latest.NodeRunID, tx)
		attrs = a
		return err
	}))
	require.NotNil(t, attrs, "message delivery must actually populate the receiver run's attribute bag")
	require.Equal(t, secretMarker, attrs.Data["secret"],
		"the payload must reach the substitution bag verbatim (named-path substitution)")

	for _, rec := range capLogger.Records() {
		require.NotContains(t, rec.Msg, secretMarker,
			"log message must never contain the raw message payload")
		for k, v := range rec.Fields {
			rendered := fmt.Sprint(v)
			require.False(t, strings.Contains(rendered, secretMarker),
				"log field %q must never carry the raw message payload; got %v", k, rendered)
		}
	}
}
