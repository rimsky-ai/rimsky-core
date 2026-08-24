// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostdaemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func TestOrphanReap_ProxyDisconnectSIGKILLsUnresponsiveChildOnRecovery(t *testing.T) {
	bin := buildStubChild(t)
	pidLog := t.TempDir() + "/pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)
	t.Setenv("STUBCHILD_IGNORE_SIGTERM", "1")

	fp := startFakeProxy(t)
	connectDaemonToFakeProxy(t, fp, Config{ReapGracePeriod: 200 * time.Millisecond})

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
	fp.sendToDaemon(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
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

	awaited.Until(t, fmt.Sprintf("the orphaned child pid %d to exit after the daemon's stream dropped", pid),
		func() bool { return processExited(pid) })
}

func readStubchildPID(t *testing.T, path, runScopeID string) int {
	t.Helper()
	pid := 0
	awaited.Until(t, "the stub child to record its pid for run scope "+runScopeID, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == runScopeID {
				parsed, convErr := strconv.Atoi(fields[1])
				if convErr != nil {
					t.Fatalf("pid log line %q: %v", line, convErr)
				}
				pid = parsed
				return true
			}
		}
		return false
	})
	return pid
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func processExited(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == syscall.ESRCH
}
