// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const serviceNameHeaderKey = "x-rimsky-service-name"

const validationRejectRoleSentinel = "stubchild-reject"

const (
	candidateHandlePrefixSentinel   = "stub-candidate:"
	committedMetadataPrefixSentinel = "stub-committed:"
)

func TestHostAgentLateBindAllProtocols(t *testing.T) {

	publishLog := t.TempDir() + "/stub-publish.log"
	t.Setenv("STUBCHILD_PUBLISH_LOG", publishLog)

	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-all-protocols")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-all", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")
	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	instanceID := iid.String()

	conn, err := grpc.NewClient(fx.proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "dial proxy")
	t.Cleanup(func() { _ = conn.Close() })

	t.Run("validation", func(t *testing.T) {
		assertLateBindValidation(t, conn, instanceID)
	})
	t.Run("publisher", func(t *testing.T) {
		assertLateBindPublisher(t, conn, instanceID, publishLog)
	})
	t.Run("data_processing", func(t *testing.T) {
		assertLateBindDataProcessing(t, conn, instanceID)
	})
}

func callCtxWithService(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx := metadata.AppendToOutgoingContext(context.Background(), serviceNameHeaderKey, lateBindServiceName)
	return context.WithTimeout(ctx, 30*time.Second)
}

func assertLateBindValidation(t *testing.T, conn *grpc.ClientConn, instanceID string) {
	t.Helper()
	ctx, cancel := callCtxWithService(t)
	defer cancel()

	client := genv1.NewValidationClient(conn)
	resp, err := client.Validate(ctx, &genv1.ValidateRequest{
		Role: validationRejectRoleSentinel,
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias: instanceID,
		}},
	})

	require.NotEqual(t, codes.Unimplemented, status.Code(err),
		"validation dispatch returned gRPC Unimplemented — the proxy did not forward to the spawned binary")
	require.NoError(t, err, "validation dispatch should be served by the real spawned validator")
	require.NotNil(t, resp, "validation response")
	require.False(t, resp.GetValid(), "stub validator must REJECT the sentinel role (a deliberately-rejecting validator)")
	require.NotEmpty(t, resp.GetErrors(), "rejecting validator must surface at least one finding")
	require.Equal(t, "stubchild_rejected", resp.GetErrors()[0].GetClass(),
		"the rejecting finding must come from the real spawned stub validator")
}

func assertLateBindPublisher(t *testing.T, conn *grpc.ClientConn, instanceID, publishLog string) {
	t.Helper()
	ctx, cancel := callCtxWithService(t)
	defer cancel()

	client := genv1.NewPublisherClient(conn)
	const subID = "pub-sub-latebind-1"
	const messageType = "lifecycle/tick"
	_, err := client.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: subID,
		InstanceId:              instanceID,
		Kind:                    "cron",
		MessageType:             messageType,
	})

	require.NotEqual(t, codes.Unimplemented, status.Code(err),
		"publisher dispatch returned gRPC Unimplemented — the proxy did not forward to the spawned binary")
	require.NoError(t, err, "publisher dispatch should be served by the real spawned publisher")

	want := strings.Join([]string{subID, instanceID, messageType}, " ")
	require.Eventually(t, func() bool {
		data, readErr := os.ReadFile(publishLog)
		if readErr != nil {
			return false
		}
		return strings.Contains(string(data), want)
	}, 10*time.Second, 100*time.Millisecond,
		"stub did not record the publish (%q) — the Subscribe dispatch never reached the spawned binary", want)
}

func assertLateBindDataProcessing(t *testing.T, conn *grpc.ClientConn, instanceID string) {
	t.Helper()
	client := genv1.NewDataProcessingClient(conn)

	const idempotencyKey = "dp-idem-latebind-1"

	beginCtx, beginCancel := callCtxWithService(t)
	defer beginCancel()
	begin, err := client.BeginCandidate(beginCtx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim-handle-latebind-1",
		IdempotencyKey: idempotencyKey,
	})
	require.NotEqual(t, codes.Unimplemented, status.Code(err),
		"data-processing BeginCandidate returned gRPC Unimplemented — the proxy did not forward to the spawned binary")
	require.NoError(t, err, "BeginCandidate should be served by the real spawned data-processor")
	require.NotNil(t, begin)

	wantHandle := candidateHandlePrefixSentinel + idempotencyKey
	require.Equal(t, wantHandle, string(begin.GetCandidateHandle()),
		"the candidate handle must come from the real spawned binary's typed-data op")

	commitCtx, commitCancel := callCtxWithService(t)
	defer commitCancel()
	commit, err := client.CommitCandidate(commitCtx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	})
	require.NotEqual(t, codes.Unimplemented, status.Code(err),
		"data-processing CommitCandidate returned gRPC Unimplemented — the proxy did not forward to the spawned binary")
	require.NoError(t, err, "CommitCandidate should be served by the real spawned data-processor")
	require.NotNil(t, commit)

	wantMetadata := committedMetadataPrefixSentinel + wantHandle
	require.Equal(t, wantMetadata, string(commit.GetCandidateMetadata()),
		"the committed candidate metadata must be the real spawned binary's deterministic derivation of the begun handle")
}
