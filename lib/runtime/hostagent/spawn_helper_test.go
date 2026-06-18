// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", spawned.Port), 2*time.Second)
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
	select {
	case <-spawned.Exited:
	case <-time.After(5 * time.Second):
		_ = spawned.Cmd.Process.Kill()
		<-spawned.Exited
		t.Fatal("child did not exit within 5s of SIGTERM")
	}
	if spawned.Cmd.ProcessState == nil {
		t.Fatal("expected ProcessState set after Exited fires")
	}
}

func TestSpawnService_ReadyTimeoutReapsChild(t *testing.T) {
	bin := buildFixture(t, "stub-no-bind")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
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

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("SpawnService took %v on readiness timeout, want < 5s (suggests no reap)", elapsed)
	}

	if got := err.Error(); !strings.Contains(got, "did not bind port") {
		t.Fatalf("error = %q, want substring %q", got, "did not bind port")
	}
}
