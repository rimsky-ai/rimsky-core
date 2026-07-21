// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"bytes"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

func TestMountBridge_RejectsOversizedBody(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	MountBridge(mux, testServer(t, true))
	id, err := peerauth.Load(t.Context(), peerauth.Config{Mode: enroll.PeerAuthNone}, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewBridgeServer(lis.Addr().String(), mux, id)
	go func() { _ = ServeBridge(srv, lis, id) }()
	t.Cleanup(func() { _ = srv.Close() })

	oversized := make([]byte, maxExecuteBodyBytes+1)
	for i := range oversized {
		oversized[i] = ' '
	}
	resp, err := http.Post("http://"+lis.Addr().String()+"/v1/Execute", "application/json", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d for a body over the size cap", resp.StatusCode, http.StatusBadRequest)
	}
}
