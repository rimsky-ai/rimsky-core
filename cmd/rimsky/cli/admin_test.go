// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func TestRunAdminReset(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	srv.State.SetTemplateState(hash, "deployed")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})
	if got := cli.RunAdminReset(context.Background(), []string{"--yes", "n1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}
