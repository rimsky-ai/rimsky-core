// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package cli_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/control/cli"
	"github.com/rimsky-ai/rimsky-core/control/cli/internal/clitest"
)

// (TestRunAdminForceFire retired by the 2026-05-15 plan B10 / D7 / E16
// schedule-retirement cascade.)

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
