// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_latebind_all_protocols_test.go — end-to-end proof that the
// host-agent-proxy is a TRANSPARENT forwarder for EVERY rimsky service
// protocol it fronts (story S-hostagent-latebind-all-protocols), not just
// executor + claim-producer. A real local binary is late-bound and a
// dispatch is driven through the REAL proxy + agent for each of the three
// remaining protocols the proxy fronts — publisher, validation,
// data-processing — and each MUST be served by the real spawned binary,
// NOT returned as gRPC Unimplemented.
//
// The supervisor does not natively route publisher / validation /
// data-processing through the late-bind proxy in v1, so this test exercises
// the proxy's supervisor-facing handlers DIRECTLY over gRPC — dialing the
// running proxy with the x-rimsky-service-name header (the same per-call
// metadata the supervisor's client interceptor stamps, as the in-process
// proxy unit harness does). The instance is created through the real
// control-api so the proxy's binding cache learns the `codegen` binding →
// stub-binary path (via OnInstanceCreated + the GET fallback) and the
// connected agent owns it. Each direct dispatch then resolves
// (instance → owner → agent → binding), lazily spawns the stub, and tunnels
// the protocol's RPC into it.
//
// RED (current tree): the proxy registers Unimplemented{Publisher,
// Validation,DataProcessing}Server (main.go:74-76 + unimplemented_handlers.go),
// so each of the three dispatches returns gRPC codes.Unimplemented straight
// from the proxy — it never resolves, never spawns, never forwards. The
// assertions (status.Code(err) != codes.Unimplemented, plus the positive
// observable outcome from the spawned binary) FAIL until a later GREEN pass
// replaces the stubs with real forwarding handlers and teaches the agent to
// handshake + dispatch these three protocols.
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

// serviceNameHeaderKey is the per-call gRPC metadata key the supervisor's
// client interceptor stamps with the resolved-for service name; the proxy's
// resolveAndSpawn reads it to look up the binding. Mirrors the proxy's
// internal serviceNameHeader const (dispatch.go).
const serviceNameHeaderKey = "x-rimsky-service-name"

// validationRejectRoleSentinel mirrors the stubchild's validationRejectRole
// sentinel: a ValidateRequest carrying this role makes the stub validator
// REJECT (valid=false with one ValidationFinding), so a deliberately-
// rejecting validator is observable through the proxy + agent tunnel.
const validationRejectRoleSentinel = "stubchild-reject"

// candidateHandlePrefixSentinel / committedMetadataPrefixSentinel mirror the
// stubchild's deterministic typed-data op: BeginCandidate echoes the
// idempotency_key into the candidate handle (prefixed), and CommitCandidate
// derives candidate_metadata from that handle (prefixed). Asserting on these
// prefixes binds the data-processing dispatch to the real spawned binary.
const (
	candidateHandlePrefixSentinel   = "stub-candidate:"
	committedMetadataPrefixSentinel = "stub-committed:"
)

func TestHostAgentLateBindAllProtocols(t *testing.T) {
	// Not parallel: execs real child processes and binds free ports; keep it
	// serial so the port reservations and process reaping stay predictable.

	// A publish log so the late-bind publisher dispatch is observably served
	// by the real spawned binary (the stub appends a line per Subscribe).
	// Set BEFORE the fixture starts so every spawned child inherits it.
	publishLog := t.TempDir() + "/stub-publish.log"
	t.Setenv("STUBCHILD_PUBLISH_LOG", publishLog)

	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	// Create an instance binding `codegen` to the stub binary and let its
	// executor node dispatch through proxy → agent → stub. Reaching fresh
	// proves the agent is connected, the binding cache is populated, and the
	// stub is spawnable — the preconditions the direct protocol dispatches
	// below rely on. (The executor + claim-producer paths already work; this
	// step is the load-bearing "already-working" baseline the story names.)
	tid := fx.deployLateBindTemplate(t, "late-bind-all-protocols")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-all", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")
	require.True(t, fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 45*time.Second),
		"late-bound executor worker did not reach fresh — agent/binding/spawn baseline not ready")

	instanceID := iid.String()

	// Dial the running proxy's supervisor-facing gRPC port directly. Each
	// client carries the x-rimsky-service-name header naming the late-bound
	// service (codegen), exactly as the supervisor's client interceptor would.
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

// callCtxWithService returns a context carrying the x-rimsky-service-name
// header set to the late-bound service name (codegen), bounded by a timeout.
func callCtxWithService(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx := metadata.AppendToOutgoingContext(context.Background(), serviceNameHeaderKey, lateBindServiceName)
	return context.WithTimeout(ctx, 30*time.Second)
}

// assertLateBindValidation drives a Validation.Validate dispatch through the
// proxy to the spawned stub. The stub REJECTS the sentinel role, so the
// REAL outcome is valid=false with the stubchild_rejected finding — NOT a
// gRPC Unimplemented from a proxy stub.
func assertLateBindValidation(t *testing.T, conn *grpc.ClientConn, instanceID string) {
	t.Helper()
	ctx, cancel := callCtxWithService(t)
	defer cancel()

	client := genv1.NewValidationClient(conn)
	resp, err := client.Validate(ctx, &genv1.ValidateRequest{
		Role: validationRejectRoleSentinel,
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias: instanceID, // any non-empty context; the role drives the verdict
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

// assertLateBindPublisher drives a Publisher.Subscribe dispatch through the
// proxy to the spawned stub. Success + a recorded publish line prove the
// dispatch was served by the real spawned binary.
func assertLateBindPublisher(t *testing.T, conn *grpc.ClientConn, instanceID, publishLog string) {
	t.Helper()
	ctx, cancel := callCtxWithService(t)
	defer cancel()

	client := genv1.NewPublisherClient(conn)
	const subID = "pub-sub-latebind-1"
	const targetNode = "receiver"
	_, err := client.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: subID,
		InstanceId:              instanceID,
		Kind:                    "cron",
		TargetNode:              targetNode,
	})

	require.NotEqual(t, codes.Unimplemented, status.Code(err),
		"publisher dispatch returned gRPC Unimplemented — the proxy did not forward to the spawned binary")
	require.NoError(t, err, "publisher dispatch should be served by the real spawned publisher")

	// The stub records "<sub_id> <instance_id> <target_node>" per Subscribe.
	want := strings.Join([]string{subID, instanceID, targetNode}, " ")
	require.Eventually(t, func() bool {
		data, readErr := os.ReadFile(publishLog)
		if readErr != nil {
			return false
		}
		return strings.Contains(string(data), want)
	}, 10*time.Second, 100*time.Millisecond,
		"stub did not record the publish (%q) — the Subscribe dispatch never reached the spawned binary", want)
}

// assertLateBindDataProcessing drives a DataProcessing BeginCandidate +
// CommitCandidate dispatch through the proxy to the spawned stub. The
// committed candidate is deterministically derived from the begun handle, so
// the returned metadata proves a real typed-data op ran in the spawned binary.
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
