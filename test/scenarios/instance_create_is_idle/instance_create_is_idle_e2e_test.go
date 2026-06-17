// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instance_create_is_idle_e2e_test.go — executable proof for
// STORY-instance-create-is-idle.
//
// Boots the in-process full stack (scheduler + supervisor + control-api
// + stub executor) via the scenario harness against a Postgres
// testcontainer, deploys a two-node template, and asserts that
// `POST /v1/instances` returns success WITHOUT enqueueing a frame,
// landing a synthetic envelope in the ledger, or materializing a
// node-run. The lifecycle-subscriber peer wired through the Stores
// catalog must receive exactly one `OnInstanceCreated` callback.
//
// The test bypasses `scenario.Harness.CreateInstance` deliberately —
// that helper now emits an internal empty-message wake after the
// create POST (per decision:test-harness-create-instance-wakes-roots-after-create)
// so the existing scenario suite's `waitForRootDispatch` semantics
// hold. To exhibit the post-spec idle-on-create behavior the test must
// drive the raw HTTP surface and observe the un-woken state directly.
//
// @story: instance-create-is-idle
// @concept: instance
// @decision: empty-message-as-root-trigger

package instance_create_is_idle

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestStory_InstanceCreateIsIdle exhibits STORY-instance-create-is-idle
// end-to-end: create the instance, observe empty frame collection and
// empty message ledger, observe no node-runs have run, observe exactly
// one lifecycle-subscriber `OnInstanceCreated` callback.
func TestStory_InstanceCreateIsIdle(t *testing.T) {
	t.Parallel()

	// @constraint: fake lifecycle-subscriber on a loopback gRPC server,
	// wired into the scenario harness's Stores catalog so the control-
	// api dials it for fan-out at instance-create time. Mirrors the
	// canary pattern in test/scenarios/canary/lifecycle_subscriber_callback_test.go.
	fake := newFakeLifecycleServer(t)
	t.Cleanup(fake.stop)

	h := scenario.Start(t, scenario.HarnessOpts{
		// @deliberate: lifecycle events fire from the control-api regardless
		// of the supervisor / scheduler state. Idle-on-create is the
		// negative assertion this test pins, so keeping the supervisor +
		// scheduler running guards the strongest form of the falsifier:
		// "if the supervisor were silently dispatching the create-side
		// envelope, a node-run row would materialize in our wait window."
		HeartbeatTimeout: 30 * time.Second,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"idle-create-lifecycle": {
					Endpoint: "grpc://" + fake.addr,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
					Protocols: []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})

	// @deliberate: two-node template: `root` is the structural root (no
	// `subscribes:` block) and `down` subscribes to root via a direct
	// (author-declared) edge. Per spec the structural-root edges keyed
	// by sender="" are runtime-injected at registration. Until an
	// operator emits an empty message, neither node has any reason to
	// dispatch — idleness must hold regardless of how many roots exist.
	tspec := node.TemplateSpec{
		Name:    "instance-create-is-idle",
		Version: "v1",
		Messages: []spec.MessageSchema{
			// @deliberate: declare a no-op typed message so the test template's
			// message-schema set is non-empty. The empty-string entry is
			// runtime-injected; this declared entry exists only so the
			// template's `messages:` block is non-trivial and exercises the
			// registration path the same way a real template would.
			{Type: "test/noop"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "root",
					Executor: "stub",
					// @constraint: store ref pulls the lifecycle-subscriber
					// peer into the template's lifecycle-fanout set
					// (`code:lib/control/controlapi/lifecycle.go::LifecyclePeersForSpec`
					// derives the per-template peer set from each node's
					// Stores + Executor references). Without a peer in the
					// fanout set the OnInstanceCreated callback has nothing
					// to dispatch against. The claim is never actually
					// opened because no dispatch happens in this idle test —
					// the ref exists purely to wire the peer into the
					// lifecycle-fanout discovery surface.
					Stores: []node.NodeStoreRef{{
						Name: "idle-create-lifecycle", Selector: "x", Intent: "r",
					}},
				},
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "down", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "root",
					Type:                 "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	}

	templateHash := h.DeployTemplate(tspec)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")

	// @constraint: post directly via the raw HTTP surface — `scenario.Harness.CreateInstance`
	// now emits an internal wake message after the create POST so existing
	// tests' `waitForRootDispatch` semantics still hold. To observe the
	// idle-on-create behavior this test MUST drive the raw POST and
	// bypass the helper's wake step.
	instanceID := postCreateInstance(t, h, templateHash, "ck-instance-create-is-idle")
	require.NotEmpty(t, instanceID, "POST /v1/instances must return a non-empty instance_id")

	// @constraint: GET /v1/instances/{id} reports paused=false and no
	// terminal timestamp. The detail GET also reflects the create's
	// observable post-conditions before any frame opens.
	detail := getInstanceDetail(t, h, instanceID)
	require.Equal(t, false, detail["paused"], "fresh instance must report paused=false")
	require.Nil(t, detail["terminated_at"], "fresh instance must carry no terminated_at")

	// @deliberate: a bounded settle window. The OnInstanceCreated callback
	// fires synchronously from the control-api handler; the supervisor's
	// frame engine ticks at ~250ms in the scenario harness. 1.5s is well
	// above either, so a missed dispatch / late-fire would surface here.
	// The assertion is strictly negative: nothing should land. A loose
	// `Eventually` would mask a slow-dispatch falsifier, so we use a
	// fixed sleep and then a strict equality.
	time.Sleep(1500 * time.Millisecond)

	// @constraint: STORY-instance-create-is-idle falsifier (1):
	// "a frame row exists for the instance with no operator-posted
	// triggering message". GET /v1/instances/{id}/frames must return
	// `frames: []` after the bounded wait.
	frames := getInstanceFrames(t, h, instanceID)
	require.Empty(t, frames, "STORY-instance-create-is-idle falsifier (1): frames collection MUST be empty until an operator posts a message; got %d frame(s)", len(frames))

	// @constraint: STORY-instance-create-is-idle falsifier (2):
	// "a synthetic envelope appears in the message ledger immediately
	// after create". GET /v1/instances/{id}/messages must return
	// `messages: []`.
	messages := getInstanceMessages(t, h, instanceID)
	require.Empty(t, messages, "STORY-instance-create-is-idle falsifier (2): message ledger MUST be empty until an operator posts a message; got %d message(s)", len(messages))

	// @constraint: STORY-instance-create-is-idle falsifier (3):
	// "a node-run row exists with no operator emission having occurred".
	// Per-instance node rows DO exist (instance-create mints one per
	// node-type, defaulting to state `fresh` with frame_id=NULL). What
	// must NOT exist is a node row whose frame_id is non-null or whose
	// state has advanced beyond `fresh` (the "a run has begun" shape).
	// The GET /v1/instances/{id}/nodes surface returns the per-instance
	// node row list.
	nodes := getInstanceNodes(t, h, instanceID)
	require.NotEmpty(t, nodes, "node row materialization is a create-time side effect; node list must NOT be empty (the row itself is created at instance-create and defaults to state=fresh)")
	for _, n := range nodes {
		row, _ := n.(map[string]any)
		require.NotNil(t, row, "node list entry must be a JSON object")
		nodeType, _ := row["node_type"].(string)
		state, _ := row["state"].(string)
		frameID, _ := row["frame_id"].(string)
		require.Equal(t, "fresh", state,
			"STORY-instance-create-is-idle falsifier (3): node %q must be in state `fresh` until an operator emits; got %q", nodeType, state)
		require.Empty(t, frameID,
			"STORY-instance-create-is-idle falsifier (3): node %q must have a NULL frame_id until an operator emits; got %q", nodeType, frameID)
	}

	// @constraint: the lifecycle-subscriber's OnInstanceCreated callback
	// fired exactly once for this create. This pins the positive surface
	// of the story's Acceptance: "the lifecycle-subscriber's
	// OnInstanceCreated callback fires once." Eventually-style poll
	// because the dispatch is asynchronous; the count assertion is
	// strict.
	require.Eventually(t, func() bool {
		return fake.countFor("OnInstanceCreated") >= 1
	}, 5*time.Second, 50*time.Millisecond,
		"lifecycle-subscriber peer must observe OnInstanceCreated for the freshly created instance")
	require.Equal(t, 1, fake.countFor("OnInstanceCreated"),
		"OnInstanceCreated must fire EXACTLY once for one create; got %d", fake.countFor("OnInstanceCreated"))
}

// postCreateInstance POSTs to /v1/instances and returns the new
// instance_id string. The harness's CreateInstance helper now emits an
// internal wake message after the create POST; this helper drives the
// raw HTTP surface to observe the un-woken idle state.
func postCreateInstance(t *testing.T, h *scenario.Harness, templateHash, instanceKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"POST /v1/instances: status=%d body=%s", resp.StatusCode, string(raw))
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoErrorf(t, json.Unmarshal(raw, &out),
		"POST /v1/instances: decode response: %s", string(raw))
	require.NotEmptyf(t, out.InstanceID, "POST /v1/instances response missing instance_id: %s", string(raw))
	return out.InstanceID
}

// getInstanceDetail GETs /v1/instances/{id} and returns the decoded JSON
// object.
func getInstanceDetail(t *testing.T, h *scenario.Harness, instanceID string) map[string]any {
	t.Helper()
	return getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID)
}

// getInstanceFrames GETs /v1/instances/{id}/frames and returns the
// `frames` array.
func getInstanceFrames(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/frames")
	frames, _ := body["frames"].([]any)
	return frames
}

// getInstanceMessages GETs /v1/instances/{id}/messages and returns the
// `messages` array.
func getInstanceMessages(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/messages")
	messages, _ := body["messages"].([]any)
	return messages
}

// getInstanceNodes GETs /v1/instances/{idOrKey}/nodes and returns the
// `nodes` array.
func getInstanceNodes(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/nodes")
	nodes, _ := body["nodes"].([]any)
	return nodes
}

func getJSONMap(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s: status=%d body=%s", url, resp.StatusCode, string(raw))
	var out map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &out), "GET %s: decode: %s", url, string(raw))
	return out
}

// fakeLifecycleServer is the same in-process gRPC peer used by the canary
// LifecycleSubscriber callback test. It serves both the
// `LifecycleSubscriber` surface (where this test observes the
// OnInstanceCreated callback) and a minimal `ClaimProducer.Capabilities`
// stub (so rimsky's startup peer-dial handshake succeeds — the runtime
// claim verbs are never invoked because no node dispatches in this test).
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

// @deliberate: LifecycleSubscriber method implementations record the
// verb / template_hash / instance_id triple so the test can assert the
// exact arrival pattern after the create.

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

// Capabilities is the only ClaimProducer verb rimsky invokes on this
// fake (at startup, for the handshake). Advertises both `claim_producer`
// and `lifecycle_subscriber` so the dial succeeds; the runtime claim
// verbs are never invoked because no node dispatches in this test.
func (f *fakeLifecycleServer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:             []string{"claim_producer", "lifecycle_subscriber"},
	}, nil
}
