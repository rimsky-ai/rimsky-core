// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const (
	testOwnerPlaintext = "rk_valid-owner-secret"
	testOwnerKeyID     = "0b5fbb3e-1111-2222-3333-444455556666"
)

func authenticatedModeControlAPIStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != whoamiPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer "+testOwnerPlaintext {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"api_key","key_id":"` + testOwnerKeyID + `","key_name":"owner"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestControlAPIVerifierMapsWhoamiOutcomes(t *testing.T) {
	srv := authenticatedModeControlAPIStub(t)
	verify := newControlAPIRegisterIdentityVerifier(srv.Client(), srv.URL)
	ctx := context.Background()

	verdict, err := verify(ctx, testOwnerPlaintext)
	if err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if verdict.kind != registerIdentityAPIKey || verdict.keyID != testOwnerKeyID {
		t.Fatalf("verdict: got %+v want {kind=api_key, keyID=%s}", verdict, testOwnerKeyID)
	}

	if _, err := verify(ctx, testOwnerKeyID); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("non-secret key id must be Unauthenticated, got %v", err)
	}
	if _, err := verify(ctx, "rk_wrong-secret"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unknown key must be Unauthenticated, got %v", err)
	}
	if _, err := verify(ctx, sillyname.AnonymousCredentialSentinel); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous register in authenticated mode must be Unauthenticated, got %v", err)
	}
}

func TestControlAPIVerifierAnonymousModeMirrorsControlAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"kind":"anonymous","key_name":"anonymous"}`))
	}))
	t.Cleanup(srv.Close)
	verify := newControlAPIRegisterIdentityVerifier(srv.Client(), srv.URL)

	verdict, err := verify(context.Background(), sillyname.AnonymousCredentialSentinel)
	if err != nil {
		t.Fatalf("anonymous register in anonymous mode rejected: %v", err)
	}
	if verdict.kind != registerIdentityAnonymous {
		t.Fatalf("verdict: got %+v want registerIdentityAnonymous", verdict)
	}
	if verdict.keyID != "" {
		t.Fatalf("verdict.keyID for anonymous must be empty, got %q", verdict.keyID)
	}
}

func TestControlAPIVerifierFailsClosed(t *testing.T) {
	verify := newControlAPIRegisterIdentityVerifier(http.DefaultClient, "")
	if _, err := verify(context.Background(), testOwnerPlaintext); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing control-api URL must be FailedPrecondition, got %v", err)
	}

	downSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(downSrv.Close)
	verify = newControlAPIRegisterIdentityVerifier(downSrv.Client(), downSrv.URL)
	if _, err := verify(context.Background(), testOwnerPlaintext); status.Code(err) != codes.Unavailable {
		t.Fatalf("control-api failure must be Unavailable, got %v", err)
	}
}

func TestRegisterUnverifiedKeyIsRejectedAndCannotDisplace(t *testing.T) {
	srv := authenticatedModeControlAPIStub(t)
	verify := newControlAPIRegisterIdentityVerifier(srv.Client(), srv.URL)
	state, client := newAgentTestServerWithVerifier(t, verify)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	legit, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("open legit stream: %v", err)
	}
	registerAndAck(t, legit, testOwnerPlaintext, "http://127.0.0.1:5001")
	legitConn, ok := state.lookupAgent(testOwnerKeyID)
	if !ok {
		t.Fatalf("verified agent should be registered under its key id")
	}

	for _, presented := range []string{testOwnerKeyID, "rk_wrong-secret"} {
		attacker, connErr := client.Connect(ctx)
		if connErr != nil {
			t.Fatalf("open attacker stream: %v", connErr)
		}
		mustSend(t, attacker, &genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
			ApiKey:     presented,
			AgentLabel: "attacker",
		}}})
		_, recvErr := attacker.Recv()
		if status.Code(recvErr) != codes.Unauthenticated {
			t.Fatalf("register with %q: expected Unauthenticated, got %v", presented, recvErr)
		}
	}

	if got, ok := state.lookupAgent(testOwnerKeyID); !ok || got != legitConn {
		t.Fatalf("legit agent connection must survive rejected register attempts")
	}

	mustSend(t, legit, &genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{Heartbeat: &genv1.HostAgentHeartbeat{SentAtUnixMs: 7}}})
	frame, err := legit.Recv()
	if err != nil || frame.GetHeartbeatAck() == nil {
		t.Fatalf("legit agent stream must stay live after rejected registers: err=%v frame=%T", err, frame.GetBody())
	}
}

func TestAdoptRoutingIdentityRejectsSameLabelCollision(t *testing.T) {
	state := newProxyState()
	srv := newAgentServer(state, presentedKeyIsIdentity)

	label := "sparkling-wombat"
	reg := &genv1.Register{
		ApiKey:               sillyname.AnonymousCredentialSentinel,
		AgentLabel:           "alpha",
		RoutingLabel:         label,
		LocalCallbackBaseUrl: "http://127.0.0.1:5001",
	}
	first, routingID, prior, err := srv.adoptRoutingIdentity(registerIdentityVerdict{kind: registerIdentityAnonymous}, reg)
	if err != nil {
		t.Fatalf("first adopt: unexpected err: %v", err)
	}
	if first == nil || routingID != label || prior != nil {
		t.Fatalf("first adopt: got first=%v routingID=%q prior=%v", first, routingID, prior)
	}

	reg2 := &genv1.Register{
		ApiKey:       sillyname.AnonymousCredentialSentinel,
		AgentLabel:   "beta",
		RoutingLabel: label,
	}
	second, _, _, err := srv.adoptRoutingIdentity(registerIdentityVerdict{kind: registerIdentityAnonymous}, reg2)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("second adopt with same routing_label must be AlreadyExists; got err=%v", err)
	}
	if second != nil {
		t.Fatalf("second adopt must not return a connection; got %v", second)
	}
	if got, ok := state.lookupAgent(label); !ok || got != first {
		t.Fatalf("first anonymous agent must still be registered after the rejected collision")
	}
}

func TestAdoptRoutingIdentityGeneratedNameCollisionRetries(t *testing.T) {
	state := newProxyState()
	srv := newAgentServer(state, presentedKeyIsIdentity)
	names := []string{"clashy-otter", "clashy-otter", "unique-otter"}
	var idx int
	srv.generateName = func() string {
		name := names[idx]
		if idx < len(names)-1 {
			idx++
		}
		return name
	}

	first, routingID, _, err := srv.adoptRoutingIdentity(registerIdentityVerdict{kind: registerIdentityAnonymous}, &genv1.Register{
		ApiKey:     sillyname.AnonymousCredentialSentinel,
		AgentLabel: "alpha",
	})
	if err != nil || first == nil || routingID != "clashy-otter" {
		t.Fatalf("first generated adopt: err=%v first=%v routingID=%q", err, first, routingID)
	}
	second, routingID2, _, err := srv.adoptRoutingIdentity(registerIdentityVerdict{kind: registerIdentityAnonymous}, &genv1.Register{
		ApiKey:     sillyname.AnonymousCredentialSentinel,
		AgentLabel: "beta",
	})
	if err != nil || second == nil || routingID2 != "unique-otter" {
		t.Fatalf("second generated adopt must skip the collision and land on the unique name; err=%v second=%v routingID=%q", err, second, routingID2)
	}
}
