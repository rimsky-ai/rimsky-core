// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runner

import (
	"net"
	"strings"
	"syscall"
	"testing"
)

func TestListen_GRPCAndHTTP(t *testing.T) {
	grpcLis, httpLis, adminLis, err := Listen("127.0.0.1", 0, 0, 0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer grpcLis.Close()
	defer httpLis.Close()
	if adminLis != nil {
		t.Fatalf("adminLis = %v, want nil when adminPort == 0", adminLis)
	}
	if grpcLis.Addr().String() == httpLis.Addr().String() {
		t.Fatalf("grpc and http listeners bound the same address: %s", grpcLis.Addr())
	}
}

func TestListen_AdminPortWhenPositive(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	freePort := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}

	grpcLis, httpLis, adminLis, err := Listen("127.0.0.1", 0, 0, freePort)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer grpcLis.Close()
	defer httpLis.Close()
	if adminLis == nil {
		t.Fatal("adminLis = nil, want a live listener when adminPort > 0")
	}
	defer adminLis.Close()
}

func TestListen_GRPCPortInUseWrapsError(t *testing.T) {
	busy, _, _, err := Listen("127.0.0.1", 0, 0, 0)
	if err != nil {
		t.Fatalf("Listen (setup): %v", err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	_, _, _, err = Listen("127.0.0.1", port, 0, 0)
	if err == nil {
		t.Fatal("Listen: expected error for a port already in use, got nil")
	}
	if !strings.Contains(err.Error(), "grpc listen") {
		t.Fatalf("Listen error = %q, want it to name the grpc listener", err.Error())
	}
}

func TestSignalContext_CancelsOnSIGTERM(t *testing.T) {
	ctx, cancel := SignalContext()
	defer cancel()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}
	<-ctx.Done()
}
