// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: publisher-subscription
package sensor

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

type recordedSubscribe struct {
	SubscriptionID string
	InstanceID     string
	Kind           string
	MessageType    string
	ResolvedConfig []byte
}

type recordingPublisherPeer struct {
	genv1.UnimplementedPublisherServer
	mu           sync.Mutex
	subscribes   []recordedSubscribe
	unsubscribes []string
	live         map[string]recordedSubscribe
}

func newRecordingPublisherPeer() *recordingPublisherPeer {
	return &recordingPublisherPeer{live: map[string]recordedSubscribe{}}
}

func (s *recordingPublisherPeer) Capabilities(context.Context, *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{{Kind: "cron"}},
		Protocols:      []string{"publisher"},
	}, nil
}

func (s *recordingPublisherPeer) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := recordedSubscribe{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		Kind:           req.GetKind(),
		MessageType:    req.GetMessageType(),
		ResolvedConfig: req.GetResolvedConfig(),
	}
	s.subscribes = append(s.subscribes, rec)
	s.live[rec.SubscriptionID] = rec
	return &genv1.SubscribeResponse{}, nil
}

func (s *recordingPublisherPeer) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsubscribes = append(s.unsubscribes, req.GetPublisherSubscriptionId())
	delete(s.live, req.GetPublisherSubscriptionId())
	return &genv1.UnsubscribeResponse{}, nil
}

func (s *recordingPublisherPeer) ListSubscriptions(context.Context, *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(s.live))
	for _, sub := range s.live {
		out = append(out, &genv1.PublisherSubscriptionDescriptor{
			PublisherSubscriptionId: sub.SubscriptionID,
			InstanceId:              sub.InstanceID,
			Kind:                    sub.Kind,
			MessageType:             sub.MessageType,
			StartedAt:               timestamppb.Now(),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func (s *recordingPublisherPeer) snapshotSubscribes() []recordedSubscribe {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedSubscribe(nil), s.subscribes...)
}

func (s *recordingPublisherPeer) snapshotUnsubscribes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.unsubscribes...)
}

func startPublisherPeer(t *testing.T, impl genv1.PublisherServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	genv1.RegisterPublisherServer(srv, impl)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func waitForSubscriptionState(t *testing.T, h *scenario.Harness, instanceID any, want string) (id, resolvedConfig string) {
	t.Helper()
	awaited.Until(t, "the instance's publisher subscription to reach state "+want, func() bool {
		var state string
		h.QueryRowSQL(`
			SELECT COALESCE(id::text, ''), COALESCE(state, ''), COALESCE(resolved_config::text, '')
			  FROM rimsky_publisher_subscriptions
			 WHERE instance_id = $1`,
			[]any{instanceID}, &id, &state, &resolvedConfig)
		return state == want
	})
	return id, resolvedConfig
}

func TestLifecycleStartStop_RealSubscriptionLifecycle(t *testing.T) {
	t.Parallel()
	peerImpl := newRecordingPublisherPeer()
	addr := startPublisherPeer(t, peerImpl)

	h := scenario.Start(t, scenario.HarnessOpts{
		Publishers: config.RemotePublishersConfig{
			Publishers: map[string]config.PublisherEntry{
				"pub-cron": {Endpoint: "grpc://" + addr, Protocols: []string{"publisher"}},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "publisher-lifecycle", Version: "1",
		Messages: []spec.MessageSchema{{Type: "sensor/tick"}},
		Publishers: []node.PublisherSpec{{
			Name:        "pub-cron",
			Kind:        "cron",
			Config:      spec.RawJSON(`{"schedule":"{{params.cron_schedule}}"}`),
			MessageType: "sensor/tick",
		}},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-publisher-lifecycle", map[string]any{
		"cron_schedule": "*/7 * * * *",
	})

	subID, resolvedCfg := waitForSubscriptionState(t, h, iid, "active")
	require.NotEmpty(t, subID)
	require.Contains(t, resolvedCfg, "*/7 * * * *",
		"resolved_config must carry the {{params.*}}-resolved value, not the raw placeholder")

	var publisherName, kind, messageType string
	h.QueryRowSQL(`
		SELECT publisher_name, kind, message_type
		  FROM rimsky_publisher_subscriptions
		 WHERE id = $1`,
		[]any{subID}, &publisherName, &kind, &messageType)
	require.Equal(t, "pub-cron", publisherName)
	require.Equal(t, "cron", kind)
	require.Equal(t, "sensor/tick", messageType)

	subs := peerImpl.snapshotSubscribes()
	require.Len(t, subs, 1, "the reconciler must deliver exactly one Subscribe RPC to the remote publisher")
	require.Equal(t, subID, subs[0].SubscriptionID)
	require.Equal(t, iid.String(), subs[0].InstanceID)
	require.Equal(t, "cron", subs[0].Kind)
	require.Equal(t, "sensor/tick", subs[0].MessageType)
	require.Contains(t, string(subs[0].ResolvedConfig), "*/7 * * * *",
		"the Subscribe RPC must carry the resolved config over the wire")

	resp, err := http.Post(h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "terminate instance must succeed")

	waitForSubscriptionState(t, h, iid, "stopped")
	unsubs := peerImpl.snapshotUnsubscribes()
	require.Equal(t, []string{subID}, unsubs,
		"instance termination must send exactly one Unsubscribe RPC for the live subscription")
}

func TestLifecycleStartStop_UnknownPublisherFailsClosed(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "publisher-lifecycle-unknown", Version: "1",
		Messages: []spec.MessageSchema{{Type: "sensor/tick"}},
		Publishers: []node.PublisherSpec{{
			Name:        "not-registered-anywhere",
			Kind:        "cron",
			Config:      spec.RawJSON(`{"schedule":"* * * * *"}`),
			MessageType: "sensor/tick",
		}},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-publisher-unknown", map[string]any{})

	_, _ = waitForSubscriptionState(t, h, iid, "failed")
	var reason string
	h.QueryRowSQL(`
		SELECT failure_reason FROM rimsky_publisher_subscriptions WHERE instance_id = $1`,
		[]any{iid}, &reason)
	require.Contains(t, reason, "not-registered-anywhere",
		"the failure reason must name the unknown publisher")
}
