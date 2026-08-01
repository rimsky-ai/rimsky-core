// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func tryReadStatusChildren(path string) ([]ChildStatus, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var snap StatusSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, false
	}
	return snap.Children, true
}

func waitForStatusChildren(t *testing.T, path string, want int) []ChildStatus {
	t.Helper()
	for {
		children, ok := tryReadStatusChildren(path)
		if ok && len(children) == want {
			return children
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// @story: host-agent-control-plane
func TestStatusFileReportsSpawnedChildrenByRunScopeAndBinding(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	statusPath := filepath.Join(t.TempDir(), "agent.status")
	connectAgentToFakeProxy(t, fp, Config{StatusFile: statusPath})

	waitForStatusChildren(t, statusPath, 0)

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		RunScopeId:          "run-scope-abc",
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	children := waitForStatusChildren(t, statusPath, 1)
	got := children[0]
	if got.SpawnID != spawnID {
		t.Fatalf("child spawn_id = %q, want %q", got.SpawnID, spawnID)
	}
	if got.RunScopeID != "run-scope-abc" {
		t.Fatalf("child run_scope_id = %q, want %q", got.RunScopeID, "run-scope-abc")
	}
	if got.Binding != bin {
		t.Fatalf("child binding = %q, want %q", got.Binding, bin)
	}

	reapVia(t, fp, spawnID, 5)

	waitForStatusChildren(t, statusPath, 0)
}
