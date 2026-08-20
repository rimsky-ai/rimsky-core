// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStory_InstanceCreateIsIdle(t *testing.T) {
	t.Parallel()

	fake := newFakeLifecycleServer(t)
	t.Cleanup(fake.stop)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
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

	tspec := node.TemplateSpec{
		Name:    "instance-create-is-idle",
		Version: "v1",
		Messages: []spec.MessageSchema{
			{Type: "test/noop"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "root",
					Executor: "stub",
					ClaimProducers: []node.NodeClaimProducerRef{{
						Name: "idle-create-lifecycle", Selector: "x", Intent: "r",
					}},
				},
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "down", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "root",
					Type:                 "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	}

	templateHash := h.DeployTemplate(tspec)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")

	instanceID := postCreateInstance(t, h, templateHash, "ck-instance-create-is-idle")
	require.NotEmpty(t, instanceID, "POST /v1/instances must return a non-empty instance_id")

	detail := getInstanceDetail(t, h, instanceID)
	require.Equal(t, false, detail["paused"], "fresh instance must report paused=false")
	require.Nil(t, detail["terminated_at"], "fresh instance must carry no terminated_at")

	h.WaitForSchedulerQuiescence()
	frames := getInstanceFrames(t, h, instanceID)
	require.Empty(t, frames, "STORY-instance-create-is-idle falsifier (1): frames collection MUST be empty until an operator posts a message; got %d frame(s)", len(frames))

	messages := getInstanceMessages(t, h, instanceID)
	require.Empty(t, messages, "STORY-instance-create-is-idle falsifier (2): message ledger MUST be empty until an operator posts a message; got %d message(s)", len(messages))

	nodes := getInstanceNodes(t, h, instanceID)
	require.NotEmpty(t, nodes, "node row materialization is a create-time side effect; node list must NOT be empty")
	for _, n := range nodes {
		row, _ := n.(map[string]any)
		require.NotNil(t, row, "node list entry must be a JSON object")
		nodeType, _ := row["node_type"].(string)
		_, hasState := row["state"]
		require.False(t, hasState,
			"STORY-instance-create-is-idle: node %q must NOT carry a synthesized `state` field on the response surface (state lives per-run, not per-node)", nodeType)
		frameID, _ := row["frame_id"].(string)
		require.Empty(t, frameID,
			"STORY-instance-create-is-idle falsifier (3): node %q must have a NULL frame_id until an operator emits; got %q", nodeType, frameID)
		summary, _ := row["run_summary"].(map[string]any)
		require.NotNil(t, summary,
			"STORY-instance-create-is-idle: node %q must carry a run_summary on the response (categorical run counts)", nodeType)
		active := jsonNumberToInt(t, summary, "active_count")
		pending := jsonNumberToInt(t, summary, "pending_count")
		fresh := jsonNumberToInt(t, summary, "fresh_count")
		failed := jsonNumberToInt(t, summary, "failed_count")
		require.Equal(t, 0, active+pending+fresh+failed,
			"STORY-instance-create-is-idle falsifier (3): node %q must have ZERO runs in every state until an operator emits; got summary active=%d pending=%d fresh=%d failed=%d",
			nodeType, active, pending, fresh, failed)
	}

	awaited.Until(t, "lifecycle-subscriber peer must observe OnInstanceCreated for the freshly created instance", func() bool {
		return fake.countFor("OnInstanceCreated") >= 1
	})
	require.Equal(t, 1, fake.countFor("OnInstanceCreated"),
		"OnInstanceCreated must fire EXACTLY once for one create; got %d", fake.countFor("OnInstanceCreated"))

	created := fake.eventFor("OnInstanceCreated")
	require.NotNil(t, created, "OnInstanceCreated event must have been recorded")
	require.Equal(t, instanceID, created.instanceID,
		"OnInstanceCreated must carry the created instance's id")
	require.Equal(t, templateHash, created.templateHash,
		"OnInstanceCreated must carry the deployed template's hash")
}

func postCreateInstance(t *testing.T, h *scenario.Harness, templateHash, instanceKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"target_agent": "scenario-default-agent",
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

func getInstanceDetail(t *testing.T, h *scenario.Harness, instanceID string) map[string]any {
	t.Helper()
	return getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID)
}

func getInstanceFrames(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/frames")
	frames, ok := body["frames"].([]any)
	require.True(t, ok, "response body missing a `frames` array key: %+v", body)
	return frames
}

func getInstanceMessages(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/messages")
	messages, ok := body["messages"].([]any)
	require.True(t, ok, "response body missing a `messages` array key: %+v", body)
	return messages
}

func getInstanceNodes(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/nodes")
	nodes, _ := body["nodes"].([]any)
	return nodes
}

// @concept: node
func jsonNumberToInt(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		t.Fatalf("jsonNumberToInt: key %q has unexpected type %T", key, raw)
		return 0
	}
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

func (f *fakeLifecycleServer) eventFor(verb string) *lifecycleEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.events) - 1; i >= 0; i-- {
		if f.events[i].verb == verb {
			ev := f.events[i]
			return &ev
		}
	}
	return nil
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
