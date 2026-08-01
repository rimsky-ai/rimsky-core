// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestOrphanReap_ProxyDisconnectSIGKILLsUnresponsiveChildOnRecovery(t *testing.T) {
	bin := buildStubChild(t)
	pidLog := t.TempDir() + "/pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)
	t.Setenv("STUBCHILD_IGNORE_SIGTERM", "1")

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{ReapGracePeriod: 200 * time.Millisecond})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	reqBytes, _ := proto.Marshal(&genv1.ExecuteRequest{NodeId: "n1", InstanceId: "inst-1", RunScopeId: "probe-scope"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolExecutor,
		Payload:  reqBytes,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
	if resp := nextDispatch(t, fp, streamID); resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("execute probe kind = %v, want DATA", resp.GetKind())
	}

	pid := readStubchildPID(t, pidLog, "probe-scope")
	if !processAlive(pid) {
		t.Fatalf("spawned child pid %d must be alive before the disconnect", pid)
	}

	fp.forceDisconnect()

	for !processExited(pid) {
		time.Sleep(5 * time.Millisecond)
	}
}

func readStubchildPID(t *testing.T, path, runScopeID string) int {
	t.Helper()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == runScopeID {
					pid, convErr := strconv.Atoi(fields[1])
					if convErr != nil {
						t.Fatalf("pid log line %q: %v", line, convErr)
					}
					return pid
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func processExited(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == syscall.ESRCH
}
