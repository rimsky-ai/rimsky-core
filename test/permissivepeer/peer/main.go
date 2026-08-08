// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: permissive-peer-build
package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const peerErrorClass = "permissive-peer/refused"

type peerExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (peerExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	if attrs := req.GetAttributes(); attrs != nil {
		if v, ok := attrs.GetFields()["outcome"]; ok && v.GetStringValue() == "fail" {
			return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
				ErrorClass: peerErrorClass,
			}}}, nil
		}
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       true,
		ChangeSummary: "permissive peer: success",
	}}}, nil
}

type peerObservability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (peerObservability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
		DeclaredErrorClasses:     []string{peerErrorClass},
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "permissive-peer: listen: %v\n", err)
		os.Exit(2)
	}

	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, peerExecutor{})
	genv1.RegisterExecutorObservabilityServer(srv, peerObservability{})

	fmt.Printf("listening=%s\n", lis.Addr().String())

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "permissive-peer: serve: %v\n", err)
		os.Exit(1)
	}
}
