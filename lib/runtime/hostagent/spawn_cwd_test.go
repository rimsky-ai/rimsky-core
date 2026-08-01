// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestDispatchExecutor_InstanceLevelSpawnCwdOverridesAgentCwd(t *testing.T) {
	bin := buildStubChild(t)
	execLog := filepath.Join(t.TempDir(), "exec.log")
	t.Setenv("STUBCHILD_EXEC_LOG", execLog)
	wantCwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		Cwd:                 wantCwd,
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	reqBytes, _ := proto.Marshal(&genv1.ExecuteRequest{NodeId: "node-cwd", InstanceId: "inst-1"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolExecutor,
		Payload:  reqBytes,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})

	resp := nextDispatch(t, fp, streamID)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("kind = %v, want DATA", resp.GetKind())
	}
	var outcome genv1.Outcome
	if err := proto.Unmarshal(resp.GetPayload(), &outcome); err != nil {
		t.Fatalf("unmarshal outcome: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Outcome{Success}, got %T", outcome.GetOutcome())
	}

	reapVia(t, fp, spawnID, 5)

	gotCwd := readStubchildExecCwd(t, execLog)
	resolvedGot, err := filepath.EvalSymlinks(gotCwd)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", gotCwd, err)
	}
	if resolvedGot != wantCwd {
		t.Fatalf("child process cwd = %q, want %q (Spawn.Cwd — the instance-level params[cwd] path — must be used when Binding.Cwd is empty)", resolvedGot, wantCwd)
	}
}

func readStubchildExecCwd(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exec log %q: %v", path, err)
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	var rec struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("decode exec log line %q: %v", line, err)
	}
	if rec.Cwd == "" {
		t.Fatalf("exec log line %q reported empty cwd", line)
	}
	return rec.Cwd
}
