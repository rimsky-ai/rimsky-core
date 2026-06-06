// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestSubscribeListUnsubscribe asserts the subscription lifecycle round-trips
// over gRPC: Capabilities advertises a kind, Subscribe records a subscription,
// ListSubscriptions returns it, and Unsubscribe removes it.
func TestSubscribeListUnsubscribe(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterPublisherServer(srv, newPublisher())
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := genv1.NewPublisherClient(conn)

	caps, err := client.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(caps.GetSupportedKinds()) == 0 {
		t.Fatal("Capabilities advertised no supported kinds")
	}

	if _, err := client.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "s1", InstanceId: "i1", Kind: exampleKind, TargetNode: "tick",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	list, err := client.ListSubscriptions(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.GetSubscriptions()) != 1 || list.GetSubscriptions()[0].GetPublisherSubscriptionId() != "s1" {
		t.Fatalf("want one subscription s1, got %+v", list.GetSubscriptions())
	}

	if _, err := client.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: "s1"}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	list, err = client.ListSubscriptions(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.GetSubscriptions()) != 0 {
		t.Fatalf("want no subscriptions after unsubscribe, got %+v", list.GetSubscriptions())
	}
}
