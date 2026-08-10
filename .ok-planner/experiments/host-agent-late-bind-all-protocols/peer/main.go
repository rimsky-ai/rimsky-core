// A local service binary a rimsky host-agent late-binds and spawns. It serves
// both peer protocols a late-bound binding can name — executor and
// claim-producer — and reports the process facts a caller needs to tell one
// spawned child from another: its own pid, its argument vector, its working
// directory and the PEER_-prefixed variables in its environment.
//
// It binds the port the agent hands it in RIMSKY_AGENT_PORT, which is the
// contract a late-bound binary owes the agent.
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
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const errorClass = "late-bound-peer/refused"

func label() string {
	if v := os.Getenv("PEER_LABEL"); v != "" {
		return v
	}
	return "late-bound-peer"
}

var callsServed int64

func facts() map[string]any {
	cwd, _ := os.Getwd()
	env := map[string]any{}
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if strings.HasPrefix(parts[0], "PEER_") && len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	args := make([]any, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		args = append(args, a)
	}
	return map[string]any{
		"served_by": label(),
		"pid":       float64(os.Getpid()),
		"calls":     float64(atomic.LoadInt64(&callsServed)),
		"cwd":       cwd,
		"args":      args,
		"env":       env,
	}
}

type executorServer struct {
	genv1.UnimplementedExecutorServer
}

func (executorServer) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	atomic.AddInt64(&callsServed, 1)
	log.Printf("execute node=%s run_scope=%s pid=%d calls=%d",
		req.GetNodeType(), req.GetRunScopeId(), os.Getpid(), atomic.LoadInt64(&callsServed))
	if v := os.Getenv("PEER_EXECUTE_DELAY_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("peer: PEER_EXECUTE_DELAY_SECONDS=%q is not a number", v)
		}
		time.Sleep(time.Duration(n) * time.Second)
	}
	if s, _ := attrs["outcome"].(string); s == "fail" {
		payload, _ := structpb.NewStruct(facts())
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: errorClass,
			Payload:    payload,
		}}}, nil
	}
	delta, err := structpb.NewStruct(facts())
	if err != nil {
		return nil, err
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:         true,
		ChangeSummary:   "served by " + label(),
		AttributesDelta: delta,
		Scratch:         req.GetScratch(),
	}}}, nil
}

type executorObservability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (executorObservability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outcome": map[string]any{"type": "string"},
		},
	})
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: schema,
		DeclaredErrorClasses:     []string{errorClass},
	}, nil
}

type producerServer struct {
	genv1.UnimplementedClaimProducerServer
}

func (producerServer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed:  []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		SupportsSplitScope:     false,
		SupportsScopesConflict: false,
		Protocols:              []string{"claim_producer"},
		DeclaredErrorClasses:   []string{errorClass},
	}, nil
}

func (producerServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	log.Printf("open claim=%s selector=%s intent=%s pid=%d",
		req.GetClaimId(), req.GetSelector(), req.GetIntent(), os.Getpid())
	scope, _ := json.Marshal(map[string]any{"selector": req.GetSelector()})
	f := facts()
	f["selector"] = req.GetSelector()
	payload, _ := json.Marshal(f)
	address, _ := json.Marshal(map[string]any{"kind": "late-bound-peer", "selector": req.GetSelector()})
	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		Address:                address,
		Payload:                payload,
		ClaimScope:             scope,
		RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
	}}}, nil
}

func (producerServer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	log.Printf("commit claim=%s pid=%d", req.GetClaimId(), os.Getpid())
	meta, _ := json.Marshal(facts())
	return &genv1.CommitResponse{VersionId: "v-" + strconv.Itoa(os.Getpid()), ProducerMetadata: meta}, nil
}

func (producerServer) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	log.Printf("abandon claim=%s pid=%d", req.GetClaimId(), os.Getpid())
	return &genv1.AbandonResponse{}, nil
}

func (producerServer) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	log.Printf("release claim=%s pid=%d", req.GetClaimId(), os.Getpid())
	return &genv1.ReleaseResponse{}, nil
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
	if v := os.Getenv("PEER_STARTUP_DELAY_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("peer: PEER_STARTUP_DELAY_SECONDS=%q is not a number: %v", v, err)
		}
		log.Printf("peer: delaying %d seconds before binding", n)
		time.Sleep(time.Duration(n) * time.Second)
	}
	if p := os.Getenv("PEER_PID_FILE"); p != "" {
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	srv, identity, err := peerauth.NewGRPCServer(ctx, label())
	if err != nil {
		log.Fatalf("peer: peer-auth setup failed: %v", err)
	}
	identity.StartMaintain(ctx, label())

	genv1.RegisterExecutorServer(srv, executorServer{})
	genv1.RegisterExecutorObservabilityServer(srv, executorObservability{})
	genv1.RegisterClaimProducerServer(srv, producerServer{})

	bind := os.Getenv("PEER_BIND")
	if bind == "" {
		bind = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", bind, port())
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("peer: listen %s: %v", addr, err)
	}
	log.Printf("peer %q pid=%d listening on %s (peer_auth_mtls=%v) args=%v",
		label(), os.Getpid(), addr, identity.Enabled(), os.Args[1:])

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("peer: serve: %v", err)
	}
}
