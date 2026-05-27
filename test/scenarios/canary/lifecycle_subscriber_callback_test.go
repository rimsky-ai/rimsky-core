// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Canary scenario — lifecycle-subscriber callback contract.
//
// Replaces what the openlineage white-box test caught beyond what its
// peer-driven rewrite in P1 covers: the six-event LifecycleSubscriber
// callback contract (`OnTemplateRegistered`, `OnTemplateDeployed`,
// `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`,
// `OnInstanceTerminated`) per `concept:lifecycle-subscriber` and
// spec `2026-05-24-repo-reorganization-design` §P2.5.
//
// Mechanism: stand up rimsky via the scenario harness; spin up a
// loopback fake LifecycleSubscriber gRPC server that captures every
// incoming event; wire the fake as a lifecycle-subscriber peer; drive
// the public control-API through the six transitions; assert each
// event arrived with the expected envelope shape.
//
// Scope:
//   - All six lifecycle verbs are invoked exactly once each (idempotency
//     surface).
//   - Each event carries the expected template_hash / instance_id.
//
// Out of scope (left to other scenarios):
//   - Retry/backoff under transient failures (covered by the
//     `lifecycle/lifecycle_e2e_test.go` idempotency-table scenario).
//   - Cross-event ordering invariants (also covered by lifecycle_e2e).
//
// The canary's role is to surface "the rimsky → LifecycleSubscriber
// wire broke" — not full lifecycle-engine coverage.

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

	"github.com/rimsky-ai/rimsky-core/control/config"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// TestCanary_LifecycleSubscriberCallbackContract drives rimsky through
// the full template/instance lifecycle against a fake
// LifecycleSubscriber peer and asserts each of the six events arrived
// at the fake with the expected envelope.
func TestCanary_LifecycleSubscriberCallbackContract(t *testing.T) {
	t.Parallel()

	// Fake lifecycle subscriber on an ephemeral loopback port. Self-
	// contained: includes a minimal `claim_producer` Capabilities
	// stub so rimsky's startup handshake at `runtime/peer/dial.go::Dial`
	// passes (the runtime claim verbs are never invoked because the
	// canary template's node is never dispatched — NoSupervisor=true).
	fake := newFakeLifecycleServer(t)
	t.Cleanup(fake.stop)

	h := scenario.Start(t, scenario.HarnessOpts{
		// Lifecycle events fire from control-api regardless of the
		// supervisor/scheduler state — drop both to speed the test.
		NoSupervisor:     true,
		NoScheduler:      true,
		HeartbeatTimeout: 30 * time.Second,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"canary-lifecycle": {
					Endpoint:     "grpc://" + fake.addr,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})

	ctx := context.Background()

	// Template references the lifecycle-subscriber peer via a store
	// ref so rimsky's per-template scope inclusion picks it up for
	// fan-out at registration time.
	spec := node.TemplateSpec{
		Name:                "canary-lifecycle-subscriber",
		Version:             "v1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{{
			Type:     "n1",
			Executor: "stub",
			Stores: []node.NodeStoreRef{{
				Name:     "canary-lifecycle",
				Selector: "x",
				Intent:   "r",
			}},
		}},
	}

	// Step 1: DeployTemplate fires OnTemplateRegistered + OnTemplateDeployed.
	templateHash := h.DeployTemplate(spec)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")

	require.True(t, fake.waitFor("OnTemplateRegistered", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateRegistered for %s", templateHash)
	require.True(t, fake.waitFor("OnTemplateDeployed", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateDeployed for %s", templateHash)

	// Step 2: CreateInstance fires OnInstanceCreated.
	instanceID := h.CreateInstance(templateHash, "canary-lifecycle-ck", nil)
	require.True(t, fake.waitFor("OnInstanceCreated", templateHash, instanceID.String(), 5*time.Second),
		"fake LifecycleSubscriber did not receive OnInstanceCreated for %s", instanceID)

	// Step 3: Mark the instance terminated (lifecycle test, not frame
	// engine) then DELETE /instances fires OnInstanceTerminated.
	require.NoError(t, h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.Instances().MarkTerminated(ctx, instanceID, tx)
	}))
	deleteAndExpectOK(t, h, "/instances/"+instanceID.String())
	require.True(t, fake.waitFor("OnInstanceTerminated", templateHash, instanceID.String(), 5*time.Second),
		"fake LifecycleSubscriber did not receive OnInstanceTerminated for %s", instanceID)

	// Step 4: POST /templates/{hash}/undeploy fires OnTemplateUndeployed.
	postAndExpectOK(t, h, "/templates/"+templateHash+"/undeploy")
	require.True(t, fake.waitFor("OnTemplateUndeployed", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateUndeployed for %s", templateHash)

	// Step 5: DELETE /templates/{hash} fires OnTemplateDeregistered.
	deleteAndExpectOK(t, h, "/templates/"+templateHash)
	require.True(t, fake.waitFor("OnTemplateDeregistered", templateHash, "", 5*time.Second),
		"fake LifecycleSubscriber did not receive OnTemplateDeregistered for %s", templateHash)

	// Idempotency surface: each event landed exactly once. (The
	// rimsky-side `rimsky_lifecycle_idempotencies` table guarantees
	// this on rimsky's side; this canary pins the per-peer arrival
	// count from the wire perspective.)
	for _, verb := range []string{
		"OnTemplateRegistered", "OnTemplateDeployed", "OnTemplateUndeployed",
		"OnTemplateDeregistered", "OnInstanceCreated", "OnInstanceTerminated",
	} {
		require.Equalf(t, 1, fake.countFor(verb),
			"fake LifecycleSubscriber received %s %d times, want exactly 1", verb, fake.countFor(verb))
	}
}

// fakeLifecycleServer is an in-process gRPC server that captures every
// LifecycleSubscriber event. It also runs a minimal ClaimProducer
// surface that delegates to the loopback stub-store so rimsky's
// startup Capabilities handshake passes.
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
	// Loopback dial check — wait for the listener to accept.
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

// LifecycleSubscriber methods ---------------------------------------------------

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

// ClaimProducer surface ---------------------------------------------------------

// Capabilities is the only ClaimProducer verb rimsky calls on this
// fake at startup. Advertises both `claim_producer` and
// `lifecycle_subscriber` protocols so the dial handshake passes; the
// runtime verbs (Open/Commit/Abandon/Release) are never invoked
// because the canary template's node is never dispatched.
func (f *fakeLifecycleServer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:             []string{"claim_producer", "lifecycle_subscriber"},
	}, nil
}

// HTTP helpers ------------------------------------------------------------------

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
