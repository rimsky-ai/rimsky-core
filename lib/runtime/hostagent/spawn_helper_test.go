// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func buildFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/"+name)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return bin
}

func TestSpawnService_HappyPath(t *testing.T) {
	bin := buildFixture(t, "stub-service")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spawned, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("SpawnService: %v", err)
	}
	if spawned == nil || spawned.Cmd == nil {
		t.Fatal("expected non-nil SpawnedService with running cmd")
	}
	if spawned.Port == 0 {
		t.Fatal("expected non-zero port")
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", spawned.Port))
	if err != nil {
		_ = spawned.Cmd.Process.Kill()
		<-spawned.Exited
		t.Fatalf("dial child: %v", err)
	}
	_ = conn.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", spawned.Port))
	if err != nil {
		_ = spawned.Cmd.Process.Kill()
		<-spawned.Exited
		t.Fatalf("http get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = spawned.Cmd.Process.Kill()
		<-spawned.Exited
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if err := spawned.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal sigterm: %v", err)
	}
	<-spawned.Exited
	if spawned.Cmd.ProcessState == nil {
		t.Fatal("expected ProcessState set after Exited fires")
	}
}

func TestSpawnService_ReadyTimeoutReapsChild(t *testing.T) {
	bin := buildFixture(t, "stub-no-bind")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spawned, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 200 * time.Millisecond,
	})
	if err == nil {
		if spawned != nil && spawned.Cmd != nil && spawned.Cmd.Process != nil {
			_ = spawned.Cmd.Process.Kill()
			<-spawned.Exited
		}
		t.Fatal("SpawnService: expected error on readiness timeout, got nil")
	}
	if spawned != nil {
		t.Fatalf("expected nil SpawnedService on error, got %+v", spawned)
	}

	if got := err.Error(); !strings.Contains(got, "did not bind port") {
		t.Fatalf("error = %q, want substring %q", got, "did not bind port")
	}
}

func TestSpawnService_ReadyDeadlineIsSpentOnceNotOncePerPortAttempt(t *testing.T) {
	bin := buildFixture(t, "stub-no-bind")
	ports := 0
	portSource := func() (int, error) {
		ports++
		return FreeLocalPort()
	}

	_, err := SpawnService(context.Background(), SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 200 * time.Millisecond,
		portSource:   portSource,
	})
	if err == nil {
		t.Fatal("SpawnService: expected an error from a child that never binds")
	}
	if ports != 1 {
		t.Fatalf("picked %d port(s) for a child that stayed alive without binding, want 1 — the port retry "+
			"exists for a collision the child reports by exiting at once, and re-spending the readiness "+
			"deadline on it multiplies the wait the caller asked for", ports)
	}
	if got := err.Error(); !strings.Contains(got, "within 200ms") {
		t.Fatalf("error = %q, want it to name the readiness deadline the caller asked for", got)
	}
}
