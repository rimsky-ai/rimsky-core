// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// claimProducerScript answers each relayed unary RPC with the matching
// response message. Open returns an Acquired result; the others return
// empty success responses.
func claimProducerScript() dispatchHandler {
	return func(protocol string, payload []byte) [][]byte {
		// Detect the request type by attempting Open first.
		var open genv1.OpenRequest
		if err := proto.Unmarshal(payload, &open); err == nil && open.GetClaimId() != "" && open.GetProducerName() != "" {
			resp, _ := proto.Marshal(&genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			}}})
			return [][]byte{resp}
		}
		// Fall through: respond with a Commit/Abandon/Release-shaped empty
		// response. All three are distinct messages but proto-unmarshal of
		// an empty message is harmless; the proxy decodes the right type.
		resp, _ := proto.Marshal(&genv1.CommitResponse{})
		return [][]byte{resp}
	}
}

// errorReason extracts the google.rpc.ErrorInfo reason from a gRPC error.
func errorReason(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info.Reason
		}
	}
	return ""
}

func TestOpenHappyPath(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"fs-claims": {Path: "./fs"}})

	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()

	resp, err := client.Open(ctx, &genv1.OpenRequest{
		ClaimId:      "claim-1",
		ProducerName: "fs-claims",
		InstanceId:   "inst-1",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if resp.GetAcquired() == nil {
		t.Fatalf("expected Acquired result")
	}
	if _, ok := ts.state.lookupClaimRoute("claim-1"); !ok {
		t.Fatalf("Open should record a claim route")
	}
}

func TestOpenThenCommit(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"fs-claims": {Path: "./fs"}})

	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()

	if _, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "claim-1", ProducerName: "fs-claims", InstanceId: "inst-1"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	// Commit routes by claim_id (no instance_id on the request).
	if _, err := client.Commit(ctx, &genv1.CommitRequest{ClaimId: "claim-1"}); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestReleaseDropsClaimRoute(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", func(protocol string, payload []byte) [][]byte {
		var open genv1.OpenRequest
		if err := proto.Unmarshal(payload, &open); err == nil && open.GetClaimId() != "" && open.GetProducerName() != "" {
			resp, _ := proto.Marshal(&genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{}}})
			return [][]byte{resp}
		}
		resp, _ := proto.Marshal(&genv1.ReleaseResponse{})
		return [][]byte{resp}
	})
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"fs-claims": {Path: "./fs"}})

	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()

	if _, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "claim-1", ProducerName: "fs-claims", InstanceId: "inst-1"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := client.Release(ctx, &genv1.ReleaseRequest{ClaimId: "claim-1"}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok := ts.state.lookupClaimRoute("claim-1"); ok {
		t.Fatalf("Release should drop the claim route")
	}
}

func TestOpenMissingServiceName(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"fs-claims": {Path: "./fs"}})

	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // no header
	defer cancel()
	_, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c", ProducerName: "fs-claims", InstanceId: "inst-1"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", status.Code(err))
	}
	if got := errorReason(err); got != errClassBindingNotFound {
		t.Fatalf("expected reason %s, got %q", errClassBindingNotFound, got)
	}
}

func TestOpenInstanceNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()
	_, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c", ProducerName: "fs-claims", InstanceId: "missing"})
	if got := errorReason(err); got != errClassBindingNotFound {
		t.Fatalf("expected reason %s, got %q", errClassBindingNotFound, got)
	}
}

func TestOpenOwnerEmpty(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	cacheReadyInstance(ts, "inst-1", "", map[string]bindingSpec{"fs-claims": {Path: "./fs"}})
	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()
	_, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c", ProducerName: "fs-claims", InstanceId: "inst-1"})
	if got := errorReason(err); got != errClassHostAgentNotConnected {
		t.Fatalf("expected reason %s, got %q", errClassHostAgentNotConnected, got)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal for host_agent_not_connected, got %v", status.Code(err))
	}
}

func TestOpenAgentNotConnected(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"fs-claims": {Path: "./fs"}}) // no agent
	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()
	_, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c", ProducerName: "fs-claims", InstanceId: "inst-1"})
	if got := errorReason(err); got != errClassHostAgentNotConnected {
		t.Fatalf("expected reason %s, got %q", errClassHostAgentNotConnected, got)
	}
}

func TestOpenBindingNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"other": {Path: "./x"}})
	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()
	_, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c", ProducerName: "fs-claims", InstanceId: "inst-1"})
	if got := errorReason(err); got != errClassBindingNotFound {
		t.Fatalf("expected reason %s, got %q", errClassBindingNotFound, got)
	}
}

func TestOpenSpawnFailed(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	fa.setSpawnFail(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"fs-claims": {Path: "./fs"}})
	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()
	_, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c", ProducerName: "fs-claims", InstanceId: "inst-1"})
	if got := errorReason(err); got != errClassSpawnFailed {
		t.Fatalf("expected reason %s, got %q", errClassSpawnFailed, got)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal for spawn_failed, got %v", status.Code(err))
	}
}

func TestCommitMissingClaimRoute(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", claimProducerScript())
	client := genv1.NewClaimProducerClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("fs-claims"), 5*time.Second)
	defer cancel()
	_, err := client.Commit(ctx, &genv1.CommitRequest{ClaimId: "unknown"})
	if got := errorReason(err); got != errClassBindingNotFound {
		t.Fatalf("expected reason %s, got %q", errClassBindingNotFound, got)
	}
}

func TestClaimProducerCapabilitiesAllSemantics(t *testing.T) {
	h := newClaimProducerHandler(newProxyState(), Config{})
	resp, err := h.Capabilities(context.Background(), &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	want := map[genv1.WriteSemantics]bool{
		genv1.WriteSemantics_WRITE_SEMANTICS_SYNC:           true,
		genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC:   true,
		genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC: true,
		genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY:      true,
	}
	for _, ws := range resp.GetWriteSemanticsAllowed() {
		delete(want, ws)
	}
	if len(want) != 0 {
		t.Fatalf("missing write-semantics in envelope: %v", want)
	}
}

func TestClaimProducerObsCapabilities(t *testing.T) {
	h := newClaimProducerObsHandler()
	if _, err := h.Capabilities(context.Background(), &genv1.GetClaimProducerCapabilitiesRequest{}); err != nil {
		t.Fatalf("obs capabilities: %v", err)
	}
}
