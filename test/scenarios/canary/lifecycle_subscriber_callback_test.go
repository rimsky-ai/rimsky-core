// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package canary

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: lifecycle-subscriber
func TestCanary_LifecycleSubscriberCallbackContract(t *testing.T) {
	t.Parallel()

	fake := newFakeLifecycleServer(t)
	t.Cleanup(fake.stop)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"canary-lifecycle": {
					Endpoint:     "grpc://" + fake.addr,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})

	spec := node.TemplateSpec{
		Name:    "canary-lifecycle-subscriber",
		Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "n1",
			Executor: "stub",
			ClaimProducers: []node.NodeClaimProducerRef{{
				Name:     "canary-lifecycle",
				Selector: "x",
				Intent:   "r",
			}},
		}},
	}

	templateHash := h.DeployTemplate(spec)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")

	fake.waitFor(t, "OnTemplateRegistered", templateHash, "")
	fake.waitFor(t, "OnTemplateDeployed", templateHash, "")

	instanceID := h.CreateInstance(templateHash, "canary-lifecycle-ck", nil)
	fake.waitFor(t, "OnInstanceCreated", templateHash, instanceID.String())

	postAndExpectOK(t, h, "/v1/instances/"+instanceID.String()+"/terminate")
	fake.waitFor(t, "OnInstanceTerminated", templateHash, instanceID.String())
	deleteAndExpectOK(t, h, "/v1/instances/"+instanceID.String())

	postAndExpectOK(t, h, "/v1/templates/"+templateHash+"/undeploy")
	fake.waitFor(t, "OnTemplateUndeployed", templateHash, "")

	deleteAndExpectOK(t, h, "/v1/templates/"+templateHash)
	fake.waitFor(t, "OnTemplateDeregistered", templateHash, "")

	for _, verb := range []string{
		"OnTemplateRegistered", "OnTemplateDeployed", "OnTemplateUndeployed",
		"OnTemplateDeregistered", "OnInstanceCreated", "OnInstanceTerminated",
	} {
		require.Equalf(t, 1, fake.countFor(verb),
			"fake LifecycleSubscriber received %s %d times, want exactly 1", verb, fake.countFor(verb))
	}
}

// @story: instance-create-is-idle
// @concept: lifecycle-subscriber
func TestCanary_NeverRanInstanceTerminationDoesNotFireOnRunScopeTerminal(t *testing.T) {
	t.Parallel()

	fake := newFakeLifecycleServer(t)
	t.Cleanup(fake.stop)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"canary-lifecycle-never-ran": {
					Endpoint:     "grpc://" + fake.addr,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})

	spec := node.TemplateSpec{
		Name:    "canary-never-ran-instance",
		Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "n1",
			Executor: "stub",
			ClaimProducers: []node.NodeClaimProducerRef{{
				Name:     "canary-lifecycle-never-ran",
				Selector: "x",
				Intent:   "r",
			}},
		}},
	}

	templateHash := h.DeployTemplate(spec)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")
	fake.waitFor(t, "OnTemplateDeployed", templateHash, "")

	instanceID := postInstanceRawExpectCreated(t, h, templateHash, "canary-never-ran-ck")
	fake.waitFor(t, "OnInstanceCreated", templateHash, instanceID)

	postAndExpectOK(t, h, "/v1/instances/"+instanceID+"/terminate")
	fake.waitFor(t, "OnInstanceTerminated", templateHash, instanceID)
	deleteAndExpectOK(t, h, "/v1/instances/"+instanceID)

	require.Equalf(t, 0, fake.countFor("OnRunScopeTerminal"),
		"instance %s was created but never posted a triggering message (instance-create-is-idle: no frame ever "+
			"opened), so it owns no frame-root run scope; terminating it must not fire OnRunScopeTerminal "+
			"(got %d deliveries)", instanceID, fake.countFor("OnRunScopeTerminal"))
}

// @concept: run-scope
// @concept: lifecycle-subscriber
func TestCanary_RanInstanceTerminationFiresOnRunScopeTerminal(t *testing.T) {
	t.Parallel()

	fake := newFakeLifecycleServer(t)
	t.Cleanup(fake.stop)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"canary-run-scope-terminal": {
					Endpoint:     "grpc://" + fake.addr,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "canary-run-scope-terminal")

	spec := node.TemplateSpec{
		Name:    "canary-run-scope-terminal",
		Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "worker",
			Executor: "stub",
			ClaimProducers: []node.NodeClaimProducerRef{{
				Name:     "canary-run-scope-terminal",
				Selector: "x",
				Intent:   "r",
			}},
		}},
	}

	templateHash := h.DeployTemplate(spec)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")
	fake.waitFor(t, "OnTemplateDeployed", templateHash, "")

	instanceID := h.CreateInstance(templateHash, "canary-run-scope-terminal-ck", nil)
	worker := h.FindNode(instanceID, "worker")
	require.NotNil(t, worker, "worker missing")
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	postAndExpectOK(t, h, "/v1/instances/"+instanceID.String()+"/terminate")

	fake.waitFor(t, "OnRunScopeTerminal", "", instanceID.String())
	deleteAndExpectOK(t, h, "/v1/instances/"+instanceID.String())

	require.GreaterOrEqualf(t, fake.countFor("OnRunScopeTerminal"), 1,
		"terminating an instance that genuinely ran a dispatch (and so owns a real frame-root run scope) "+
			"must fire OnRunScopeTerminal, unlike the never-ran case above")
}

type fakeLifecycleServer struct {
	genv1.UnimplementedLifecycleSubscriberServer
	genv1.UnimplementedClaimProducerServer

	addr    string
	grpcSrv *grpc.Server
	mu      sync.Mutex
	events  []lifecycleEvent
}

type lifecycleEvent struct {
	verb         string
	templateHash string
	instanceID   string
}

func newFakeLifecycleServer(t *testing.T) *fakeLifecycleServer {
	t.Helper()
	f := &fakeLifecycleServer{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f.addr = lis.Addr().String()
	f.grpcSrv = grpc.NewServer()
	genv1.RegisterLifecycleSubscriberServer(f.grpcSrv, f)
	genv1.RegisterClaimProducerServer(f.grpcSrv, f)
	go func() { _ = f.grpcSrv.Serve(lis) }()
	awaited.Until(t, "the fake lifecycle-subscriber server to accept connections on "+f.addr, func() bool {
		conn, dErr := net.DialTimeout("tcp", f.addr, 200*time.Millisecond)
		if dErr != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	return f
}

func (f *fakeLifecycleServer) stop() {
	if f.grpcSrv != nil {
		f.grpcSrv.GracefulStop()
	}
}

func (f *fakeLifecycleServer) record(ev lifecycleEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeLifecycleServer) waitFor(t *testing.T, verb, templateHash, instanceID string) {
	t.Helper()
	awaited.Until(t, "a "+verb+" lifecycle callback for template "+templateHash+" instance "+instanceID, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, ev := range f.events {
			if ev.verb == verb && ev.templateHash == templateHash && (instanceID == "" || ev.instanceID == instanceID) {
				return true
			}
		}
		return false
	})
}

func (f *fakeLifecycleServer) countFor(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, ev := range f.events {
		if ev.verb == verb {
			n++
		}
	}
	return n
}

func (f *fakeLifecycleServer) OnTemplateRegistered(_ context.Context, req *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{verb: "OnTemplateRegistered", templateHash: req.GetTemplateHash()})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) OnTemplateDeployed(_ context.Context, req *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{verb: "OnTemplateDeployed", templateHash: req.GetTemplateHash()})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) OnTemplateUndeployed(_ context.Context, req *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{verb: "OnTemplateUndeployed", templateHash: req.GetTemplateHash()})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) OnTemplateDeregistered(_ context.Context, req *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{verb: "OnTemplateDeregistered", templateHash: req.GetTemplateHash()})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) OnInstanceCreated(_ context.Context, req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{
		verb:         "OnInstanceCreated",
		templateHash: req.GetTemplateHash(),
		instanceID:   req.GetInstanceId(),
	})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) OnInstanceTerminated(_ context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{
		verb:         "OnInstanceTerminated",
		templateHash: req.GetTemplateHash(),
		instanceID:   req.GetInstanceId(),
	})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) OnRunScopeTerminal(_ context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	f.record(lifecycleEvent{
		verb:       "OnRunScopeTerminal",
		instanceID: req.GetInstanceId(),
	})
	return &genv1.LifecycleAck{}, nil
}

func (f *fakeLifecycleServer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:             []string{"claim_producer", "lifecycle_subscriber"},
	}, nil
}

func (f *fakeLifecycleServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	addr, _ := json.Marshal(req.GetClaimId())
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                addr,
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			},
		},
	}, nil
}

func (f *fakeLifecycleServer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (f *fakeLifecycleServer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (f *fakeLifecycleServer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}

func postAndExpectOK(t *testing.T, h *scenario.Harness, path string) {
	t.Helper()
	resp, err := http.Post(h.ControlBase+path, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "POST %s: status=%d", path, resp.StatusCode)
}

func deleteAndExpectOK(t *testing.T, h *scenario.Harness, path string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, h.ControlBase+path, nil)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "DELETE %s: status=%d", path, resp.StatusCode)
}

func postInstanceRawExpectCreated(t *testing.T, h *scenario.Harness, templateHash, instanceKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"target_agent": "scenario-default-agent",
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equalf(t, http.StatusCreated, resp.StatusCode, "POST /v1/instances: status=%d", resp.StatusCode)
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.InstanceID)
	return out.InstanceID
}
