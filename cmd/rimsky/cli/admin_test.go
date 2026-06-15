// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func TestRunAdminInvalidate(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "v1", "")
	srv.State.SetTemplateState(hash, "deployed")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", State: "fresh"})
	if got := cli.RunAdminInvalidate(context.Background(), []string{"--reason", "boom", "n1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunAdminReset(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "v1", "")
	srv.State.SetTemplateState(hash, "deployed")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", State: "fresh"})
	if got := cli.RunAdminReset(context.Background(), []string{"n1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	_ = clitest.Server{}
}
