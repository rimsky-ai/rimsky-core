// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/awaited"
)

func servicesModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("servicesModuleRoot: go.mod not found walking up from working dir")
		}
		dir = parent
	}
}

func buildClaudeAgentBinary(t *testing.T) string {
	t.Helper()
	root := servicesModuleRoot(t)
	out := filepath.Join(t.TempDir(), "claude-agent")
	cmd := exec.Command("go", "build", "-o", out, "./executors/claude-agent/cmd")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build claude-agent: %v\n%s", err, combined)
	}
	return out
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if err := lis.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	return port
}

func dialCapabilities(t *testing.T, addr string, deadline time.Duration) error {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	//nolint:testwallclock-outcome this bound covers one dial attempt inside a poll that returns only on success; the negative assertion dials a port with no listener, so the dial is refused, never timed out
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	_, err = genv1.NewExecutorObservabilityClient(conn).Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	return err
}

func TestClaudeAgentBinaryHonorsRimskyDaemonPort(t *testing.T) {
	bin := buildClaudeAgentBinary(t)

	daemonPort := freeTCPPort(t)
	ignoredPort := freeTCPPort(t)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"RIMSKY_DAEMON_PORT="+strconv.Itoa(daemonPort),
		"RIMSKY_EXECUTOR_PORT_GRPC="+strconv.Itoa(ignoredPort),
		"RIMSKY_EXECUTOR_HOST=127.0.0.1",
		"RIMSKY_EXECUTOR_STUB_MODE=1",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start claude-agent: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-exited
	})

	awaited.Until(t, fmt.Sprintf("claude-agent to serve capabilities on RIMSKY_DAEMON_PORT %d", daemonPort), func() bool {
		select {
		case <-exited:
			t.Fatalf("claude-agent exited before binding RIMSKY_DAEMON_PORT %d", daemonPort)
		default:
		}
		return dialCapabilities(t, "127.0.0.1:"+strconv.Itoa(daemonPort), time.Second) == nil
	})

	if err := dialCapabilities(t, "127.0.0.1:"+strconv.Itoa(ignoredPort), 500*time.Millisecond); err == nil {
		t.Fatalf("claude-agent unexpectedly bound the fallback RIMSKY_EXECUTOR_PORT_GRPC %d when RIMSKY_DAEMON_PORT was set", ignoredPort)
	}
}
