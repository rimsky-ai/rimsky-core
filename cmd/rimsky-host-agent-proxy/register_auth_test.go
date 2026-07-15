// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	identity, err := verify(ctx, testOwnerPlaintext)
	if err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if identity != testOwnerKeyID {
		t.Fatalf("routing identity: got %q want %q", identity, testOwnerKeyID)
	}

	if _, err := verify(ctx, testOwnerKeyID); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("non-secret key id must be Unauthenticated, got %v", err)
	}
	if _, err := verify(ctx, "rk_wrong-secret"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unknown key must be Unauthenticated, got %v", err)
	}
	if _, err := verify(ctx, anonymousRoutingIdentity); status.Code(err) != codes.Unauthenticated {
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

	identity, err := verify(context.Background(), anonymousRoutingIdentity)
	if err != nil {
		t.Fatalf("anonymous register in anonymous mode rejected: %v", err)
	}
	if identity != anonymousRoutingIdentity {
		t.Fatalf("routing identity: got %q want %q", identity, anonymousRoutingIdentity)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

	for _, presented := range []string{testOwnerKeyID, "rk_wrong-secret", anonymousRoutingIdentity} {
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
	if _, ok := state.lookupAgent(anonymousRoutingIdentity); ok {
		t.Fatalf("rejected anonymous register must not create a routing entry")
	}

	mustSend(t, legit, &genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{Heartbeat: &genv1.HostAgentHeartbeat{SentAtUnixMs: 7}}})
	frame, err := legit.Recv()
	if err != nil || frame.GetHeartbeatAck() == nil {
		t.Fatalf("legit agent stream must stay live after rejected registers: err=%v frame=%T", err, frame.GetBody())
	}
}
