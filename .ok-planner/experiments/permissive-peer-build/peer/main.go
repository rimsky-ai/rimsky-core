// A third-party rimsky peer. Its only rimsky dependency is the permissively
// licensed protocols module; nothing here imports the root module, the
// foundation module, or the services module.
//
// It implements the executor protocol (Execute plus the observability
// capability probe), serves plaintext or mutually-authenticated TLS depending
// on the peer-auth environment, and enrolls for its own serving credentials
// when peer auth is on.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const errorClass = "third-party/refused"

type executor struct {
	genv1.UnimplementedExecutorServer
	label string
}

func (e executor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	log.Printf("execute node=%s outcome=%v", req.GetNodeType(), attrs["outcome"])
	if s, _ := attrs["outcome"].(string); s == "fail" {
		payload, _ := structpb.NewStruct(map[string]any{
			"reason": "the node asked this peer to refuse",
			"peer":   e.label,
		})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: errorClass,
			Payload:    payload,
		}}}, nil
	}
	delta, err := structpb.NewStruct(map[string]any{
		"served_by": e.label,
		"echo":      fmt.Sprint(attrs["echo"]),
	})
	if err != nil {
		return nil, err
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:         true,
		ChangeSummary:   "served by " + e.label,
		AttributesDelta: delta,
		Scratch:         req.GetScratch(),
	}}}, nil
}

type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outcome": map[string]any{"type": "string"},
			"echo":    map[string]any{"type": "string"},
		},
	})
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: schema,
		DeclaredErrorClasses:     []string{errorClass},
	}, nil
}

func port() int {
	for _, key := range []string{"RIMSKY_AGENT_PORT", "PEER_PORT"} {
		if v := os.Getenv(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				log.Fatalf("peer: %s=%q is not a port number: %v", key, v, err)
			}
			return n
		}
	}
	return 9400
}

func main() {
	label := os.Getenv("PEER_LABEL")
	if label == "" {
		label = "third-party-peer"
	}
	bind := os.Getenv("PEER_BIND")
	if bind == "" {
		bind = "0.0.0.0"
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	srv, identity, err := peerauth.NewGRPCServer(ctx, label)
	if err != nil {
		log.Fatalf("peer: peer-auth setup failed: %v", err)
	}
	identity.StartMaintain(ctx, label)

	genv1.RegisterExecutorServer(srv, executor{label: label})
	genv1.RegisterExecutorObservabilityServer(srv, observability{})

	addr := fmt.Sprintf("%s:%d", bind, port())
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("peer: listen %s: %v", addr, err)
	}
	log.Printf("peer %q listening on %s (peer_auth_mtls=%v)", label, addr, identity.Enabled())

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("peer: serve: %v", err)
	}
	time.Sleep(0)
}
