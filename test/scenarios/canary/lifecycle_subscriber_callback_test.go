// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package canary

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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

	ctx := context.Background()

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

	require.True(t, fake.waitFor("OnTemplateRegistered", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateRegistered for %s", templateHash)
	require.True(t, fake.waitFor("OnTemplateDeployed", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateDeployed for %s", templateHash)

	instanceID := h.CreateInstance(templateHash, "canary-lifecycle-ck", nil)
	require.True(t, fake.waitFor("OnInstanceCreated", templateHash, instanceID.String(), 5*time.Second),
		"fake LifecycleSubscriber did not receive OnInstanceCreated for %s", instanceID)

	require.NoError(t, h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.Instances().MarkTerminated(ctx, instanceID, tx)
	}))
	deleteAndExpectOK(t, h, "/v1/instances/"+instanceID.String())
	require.True(t, fake.waitFor("OnInstanceTerminated", templateHash, instanceID.String(), 5*time.Second),
		"fake LifecycleSubscriber did not receive OnInstanceTerminated for %s", instanceID)

	postAndExpectOK(t, h, "/v1/templates/"+templateHash+"/undeploy")
	require.True(t, fake.waitFor("OnTemplateUndeployed", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateUndeployed for %s", templateHash)

	deleteAndExpectOK(t, h, "/v1/templates/"+templateHash)
	require.True(t, fake.waitFor("OnTemplateDeregistered", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateDeregistered for %s", templateHash)

	for _, verb := range []string{
		"OnTemplateRegistered", "OnTemplateDeployed", "OnTemplateUndeployed",
		"OnTemplateDeregistered", "OnInstanceCreated", "OnInstanceTerminated",
	} {
		require.Equalf(t, 1, fake.countFor(verb),
			"fake LifecycleSubscriber received %s %d times, want exactly 1", verb, fake.countFor(verb))
	}
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
	require.Eventually(t, func() bool {
		conn, dErr := net.DialTimeout("tcp", f.addr, 200*time.Millisecond)
		if dErr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 2*time.Second, 25*time.Millisecond)
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

func (f *fakeLifecycleServer) waitFor(verb, templateHash, instanceID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, ev := range f.events {
			if ev.verb == verb && ev.templateHash == templateHash && (instanceID == "" || ev.instanceID == instanceID) {
				f.mu.Unlock()
				return true
			}
		}
		f.mu.Unlock()
		time.Sleep(25 * time.Millisecond)
	}
	return false
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

func (f *fakeLifecycleServer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:             []string{"claim_producer", "lifecycle_subscriber"},
	}, nil
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
