// A third-party rimsky executor, reused from the permissive-peer-build
// experiment, which settles a dispatch every way the protocol allows so the
// event ledger can be read for pairing: success, error, error-then-retry,
// and a park that resumes shortly and then succeeds.
//
// Its only rimsky dependency is the permissively licensed protocols module.
package main

import (
	"context"
	"sync"

	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const (
	refusedClass = "third-party/refused"
	brokenClass  = "third-party/broken"
	servedTag    = "third-party.served"
	refusedTag   = "third-party.refused"
)

var attributesSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "outcome": {"type": "string", "default": "ok"},
    "echo": {"type": "string", "default": "hello"},
    "sleep_ms": {"type": "integer", "default": 0},
    "emit_tag": {"type": "string", "default": ""},
    "served_by": {"type": "string", "readOnly": true}
  }
}`)

type executor struct {
	genv1.UnimplementedExecutorServer
	label string

	mu     sync.Mutex
	parked map[string]bool
}

func (e *executor) parkOnce(nodeID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.parked[nodeID] {
		return false
	}
	e.parked[nodeID] = true
	return true
}

func (e *executor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	outcome, _ := attrs["outcome"].(string)
	log.Printf("execute node=%s dispatch=%s outcome=%v", req.GetNodeType(), req.GetDispatchId(), outcome)

	if ms, ok := attrs["sleep_ms"].(float64); ok && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}

	var tags []string
	if t, _ := attrs["emit_tag"].(string); t != "" {
		tags = []string{t}
	}

	switch outcome {
	case "fail":
		payload, _ := structpb.NewStruct(map[string]any{
			"reason": "the node asked this peer to refuse",
			"peer":   e.label,
		})
		if tags == nil {
			tags = []string{refusedTag}
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: refusedClass,
			Payload:    payload,
			Tags:       tags,
		}}}, nil
	case "broken":
		payload, _ := structpb.NewStruct(map[string]any{"peer": e.label})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: brokenClass,
			Payload:    payload,
		}}}, nil
	case "park_once":
		if e.parkOnce(req.GetNodeId()) {
			resume := time.Now().Add(2 * time.Second)
			return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
				ResumeAt: timestamppb.New(resume),
				Scratch:  req.GetScratch(),
			}}}, nil
		}
	case "park":
		resume := time.Now().Add(24 * time.Hour)
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
			ResumeAt: timestamppb.New(resume),
			Scratch:  req.GetScratch(),
		}}}, nil
	}

	delta, err := structpb.NewStruct(map[string]any{
		"served_by": e.label,
		"echo":      fmt.Sprint(attrs["echo"]),
	})
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{servedTag}
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:         true,
		ChangeSummary:   "served by " + e.label,
		AttributesDelta: delta,
		Scratch:         req.GetScratch(),
		Tags:            tags,
	}}}, nil
}

type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: attributesSchema,
		DeclaredErrorClasses:     []string{refusedClass, brokenClass},
		DeclaredTags:             []string{servedTag, refusedTag},
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

	genv1.RegisterExecutorServer(srv, &executor{label: label, parked: map[string]bool{}})
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
}
