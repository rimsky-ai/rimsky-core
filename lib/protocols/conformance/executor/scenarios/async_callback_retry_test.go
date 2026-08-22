// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package scenarios_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const refusalsBeforeTheReceiverAccepts = 3

// @concept: executor
func TestARefusedAsyncCallbackIsRetriedUntilTheReceiverAccepts(t *testing.T) {
	addr := startStubModeExecutor(t)

	receiver, err := conformance.StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	t.Cleanup(func() { _ = receiver.Close() })

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial the stub-mode executor: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	attrs, err := structpb.NewStruct(map[string]any{stubmode.AsyncProbeAttribute: true})
	if err != nil {
		t.Fatalf("build attributes: %v", err)
	}

	refused := receiver.SimulateRestart()

	outcome, err := genv1.NewExecutorClient(conn).Execute(context.Background(), &genv1.ExecuteRequest{
		NodeId:      "async-callback-retry",
		InstanceId:  "async-callback-retry",
		NodeType:    "conformance",
		Attributes:  attrs,
		CallbackUrl: receiver.URL(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	await, ok := outcome.GetOutcome().(*genv1.Outcome_AwaitAsync)
	if !ok {
		t.Fatalf("outcome = %T, want Outcome_AwaitAsync", outcome.GetOutcome())
	}
	ackID := await.AwaitAsync.GetAsyncAckId()
	if ackID == "" {
		t.Fatal("AwaitAsyncCallback carried an empty async_ack_id")
	}
	settled := receiver.Register(ackID)

	for seen := 0; seen < refusalsBeforeTheReceiverAccepts; {
		if <-refused == ackID {
			seen++
		}
	}
	receiver.EndSimulatedRestart()
	<-settled

	attempts := len(receiver.AttemptTimes(ackID))
	if attempts < refusalsBeforeTheReceiverAccepts+1 {
		t.Fatalf("the receiver recorded %d delivery attempts, want the %d it refused plus the one it accepted; "+
			"an executor whose callback is refused keeps retrying", attempts, refusalsBeforeTheReceiverAccepts)
	}
}
