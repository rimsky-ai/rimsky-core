// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestCallback_RegistryMiss_RecoversParentedSubClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const producerName = "cbk-store"
	const supID = "sup-CBK"

	reg := locks.NewRegistry()
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(_ claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
			},
		}, nil
	}
	reg.Add(producerName, store)

	clk := newTickClock(time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC))
	seedArgs := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 clk,
		SupervisorID:          supID,
	}

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "callback-restart-recovery", Version: "1",
	})
	ck := "ck-cbk"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode, leafNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmplRow.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		l, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "leaf", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		leafNode = l
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)
	leafNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), leafNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope"`)
	intent := "rw"
	producerNameRef := producerName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerNameRef,
			ClaimScopeData:     parentScope,
			Address:            json.RawMessage(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: supID,
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          clk.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, seedArgs, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        producerName,
			NodeRunID:           leafNodeRunID,
			HolderNodeID:        leafNode.ID,
			HolderSupervisorID:  supID,
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		}, tx)
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 1, "one sub-scope → one parented sub-claim held by the leaf run")

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.NodeAttributes().SetDispatchInputBag(ctx, leafNodeRunID, leafNode.ID, map[string]any{"seed": "base"}, tx); err != nil {
			return err
		}
		claimed, err := d.Queue().ClaimDispatchRow(ctx, leafNodeRunID, supID, tx)
		if err != nil {
			return err
		}
		require.True(t, claimed, "leaf run must be claimable")
		promoted, err := d.Queue().PromoteClaimedToRunning(ctx, leafNodeRunID, supID, tx)
		if err != nil {
			return err
		}
		require.True(t, promoted, "leaf run must promote to running")
		return d.Queue().RegisterAsyncAck(ctx, leafNodeRunID, ackID, clk.Now(), nil, nil, "", "", tx)
	}))

	cb := &runtime.CallbackServer{
		Registry:              runtime.NewCallbackRegistry(),
		Persist:               backend,
		Queue:                 d.Queue(),
		AdvisoryLocker:        d.AdvisoryLocker(),
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Clock:                 clk,
		Logger:                shared.SilentLogger{},
		SupervisorID:          supID,
	}
	cb.ProducerVerbKick = func() {
		_, _ = runtime.FlushProducerVerbOutbox(context.Background(), seedArgs)
	}
	addr, err := cb.Start("127.0.0.1", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cb.Close(context.Background()) })

	body := []byte(`{"success":{"changed":true,"attributes_delta":{"k":"v"}}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/callback/"+ackID, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "callback must settle after restart; body=%s", respBody)

	var ack struct {
		AckStatus string `json:"ack_status"`
	}
	require.NoError(t, json.Unmarshal(respBody, &ack))
	require.Equal(t, "accepted", ack.AckStatus, "recovered callback must accept and settle the run")

	var subRow, parentRow *persistence.ClaimHandleRow
	var finalAttrs *persistence.NodeAttributesRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		s, err := backend.ClaimHandles().Get(ctx, subClaims[0].ClaimHandleID, tx)
		if err != nil {
			return err
		}
		subRow = s
		p, err := backend.ClaimHandles().Get(ctx, parentClaimID, tx)
		if err != nil {
			return err
		}
		parentRow = p
		fa, err := backend.NodeAttributes().GetByRun(ctx, leafNodeRunID, tx)
		if err != nil {
			return err
		}
		finalAttrs = fa
		return nil
	}))

	require.NotNil(t, subRow)
	require.Equal(t, spec.ClaimHandleStateCommitted, subRow.State,
		"resolveLinkedSubClaimsInTx must commit the parented sub-claim through the reconstructed ClaimProducerRegistry")
	require.NotNil(t, parentRow)
	require.Equal(t, spec.ClaimHandleStateCommitted, parentRow.State,
		"the sub-claim's settlement must aggregate up and commit the parent claim")

	require.NotNil(t, finalAttrs, "final attributes must be persisted on settle")
	require.Equal(t, "base", finalAttrs.Data["seed"],
		"reconstructed ResolvedAttributes must carry the dispatch-input bag base")
	require.Equal(t, "v", finalAttrs.Data["k"],
		"the callback delta must merge onto the reconstructed base attributes")

	commits := 0
	for _, c := range store.Calls() {
		if c.Verb == "commit" && string(c.ClaimID) == parentClaimID.String() {
			commits++
		}
	}
	require.Equal(t, 1, commits, "the parent's aggregate Commit must fire exactly once")
}

const ackID = "ack-restart-recovery-fixed"
