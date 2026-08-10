// A third-party rimsky lifecycle subscriber, built the same way as the
// permissive-peer-build experiment's peer: its own Go module whose only rimsky
// requirement is the permissively licensed protocols module.
//
// It serves all seven lifecycle callbacks and records each one with the
// context rimsky handed it. It also serves the executor protocol, because a
// lifecycle subscriber is registered as a protocol alongside another peer role
// and only peers a template names receive the callbacks.
//
//	GET /state  -> every callback received, in order, with its payload
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type callback struct {
	Name    string         `json:"callback"`
	Payload map[string]any `json:"payload"`
}

type recorder struct {
	genv1.UnimplementedLifecycleSubscriberServer

	mu        sync.Mutex
	callbacks []callback
}

func (r *recorder) record(name string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbacks = append(r.callbacks, callback{Name: name, Payload: payload})
	blob, _ := json.Marshal(payload)
	log.Printf("lifecycle %s %s", name, string(blob))
}

func (r *recorder) snapshot() []callback {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]callback, len(r.callbacks))
	copy(out, r.callbacks)
	return out
}

func (r *recorder) OnTemplateRegistered(_ context.Context, req *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	r.record("OnTemplateRegistered", map[string]any{
		"template_hash": req.GetTemplateHash(),
		"spec_bytes":    len(req.GetSpec()),
		"spec_name":     specName(req.GetSpec()),
	})
	return &genv1.LifecycleAck{}, nil
}

func (r *recorder) OnTemplateDeployed(_ context.Context, req *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	r.record("OnTemplateDeployed", map[string]any{
		"template_hash": req.GetTemplateHash(),
		"tags":          req.GetTags(),
	})
	return &genv1.LifecycleAck{}, nil
}

func (r *recorder) OnTemplateUndeployed(_ context.Context, req *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	r.record("OnTemplateUndeployed", map[string]any{"template_hash": req.GetTemplateHash()})
	return &genv1.LifecycleAck{}, nil
}

func (r *recorder) OnTemplateDeregistered(_ context.Context, req *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	r.record("OnTemplateDeregistered", map[string]any{"template_hash": req.GetTemplateHash()})
	return &genv1.LifecycleAck{}, nil
}

func (r *recorder) OnInstanceCreated(_ context.Context, req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	r.record("OnInstanceCreated", map[string]any{
		"instance_id":             req.GetInstanceId(),
		"template_hash":           req.GetTemplateHash(),
		"instance_key":            req.GetInstanceKey(),
		"params":                  string(req.GetParams()),
		"service_bindings":        string(req.GetServiceBindings()),
		"owner_api_key_id":        req.GetOwnerApiKeyId(),
		"target_routing_identity": req.GetTargetRoutingIdentity(),
	})
	return &genv1.LifecycleAck{}, nil
}

func (r *recorder) OnInstanceTerminated(_ context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	r.record("OnInstanceTerminated", map[string]any{
		"instance_id":            req.GetInstanceId(),
		"template_hash":          req.GetTemplateHash(),
		"terminated_at_unix_ms":  req.GetTerminatedAtUnixMs(),
		"terminated_at_is_set":   req.GetTerminatedAtUnixMs() > 0,
		"terminated_instance_id": req.GetInstanceId(),
	})
	return &genv1.LifecycleAck{}, nil
}

func (r *recorder) OnRunScopeTerminal(_ context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	r.record("OnRunScopeTerminal", map[string]any{
		"run_scope_id":    req.GetRunScopeId(),
		"terminal_reason": req.GetTerminalReason(),
		"instance_id":     req.GetInstanceId(),
	})
	return &genv1.LifecycleAck{}, nil
}

func specName(raw []byte) string {
	var probe struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Name
}

func envPort(key string, dflt int) int {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("lifecycle-peer: %s=%q is not a port number", key, v)
	}
	return n
}

func main() {
	label := os.Getenv("PEER_LABEL")
	if label == "" {
		label = "lifecycle-peer"
	}
	ctx := context.Background()
	rec := &recorder{}

	srv, identity, err := peerauth.NewGRPCServer(ctx, label)
	if err != nil {
		log.Fatalf("lifecycle-peer: peer-auth setup failed: %v", err)
	}
	identity.StartMaintain(ctx, label)
	genv1.RegisterExecutorServer(srv, executor{label: label})
	genv1.RegisterExecutorObservabilityServer(srv, observability{})
	genv1.RegisterLifecycleSubscriberServer(srv, rec)

	mux := http.NewServeMux()
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(rec.snapshot())
	})
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", envPort("PEER_HTTP_PORT", 9601)), mux); err != nil {
			log.Fatalf("lifecycle-peer: http: %v", err)
		}
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", envPort("PEER_PORT", 9600))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("lifecycle-peer: listen %s: %v", addr, err)
	}
	log.Printf("lifecycle-peer listening on %s (peer_auth_mtls=%v)", addr, identity.Enabled())
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("lifecycle-peer: serve: %v", err)
	}
}
