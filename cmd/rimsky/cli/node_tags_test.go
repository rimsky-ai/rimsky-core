// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func instanceWithTaggedNodes(t *testing.T, srv *clitest.Server) string {
	t.Helper()
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{
		ID: "extract", InstanceID: inst.ID, NodeType: "http-node", Tags: []string{"stage:ingest", "owner:data"},
	})
	srv.State.AddNode(inst.ID, cli.Node{
		ID: "verify", InstanceID: inst.ID, NodeType: "verifier-http", Tags: []string{"stage:check"},
	})
	srv.State.AddNode(inst.ID, cli.Node{
		ID: "publish", InstanceID: inst.ID, NodeType: "http-node",
	})
	return inst.ID
}

// @concept: node
func TestInstanceNodesShowsTheTagsTheServerReturns(t *testing.T) {
	srv := setupClitest(t)
	id := instanceWithTaggedNodes(t, srv)

	var code int
	out := captureStdout(t, func() {
		code = cli.RunInstanceNodes(context.Background(), []string{id})
	})
	if code != 0 {
		t.Fatalf("instance nodes: exit %d, want 0", code)
	}
	if !strings.Contains(out, "TAGS") {
		t.Errorf("instance nodes: stdout %q carries no TAGS column", out)
	}
	if !strings.Contains(out, "stage:ingest,owner:data") {
		t.Errorf("instance nodes: stdout %q never shows the tags a node carries", out)
	}
}

// @concept: node
func TestInstanceNodesSelectsByTag(t *testing.T) {
	srv := setupClitest(t)
	id := instanceWithTaggedNodes(t, srv)

	out := captureStdout(t, func() {
		if code := cli.RunInstanceNodes(context.Background(), []string{"--tag", "stage:check", id}); code != 0 {
			t.Fatalf("instance nodes --tag: exit %d", code)
		}
	})
	if !strings.Contains(out, "verify") {
		t.Errorf("instance nodes --tag stage:check: stdout %q, want the node carrying that tag", out)
	}
	if strings.Contains(out, "extract") || strings.Contains(out, "publish") {
		t.Errorf("instance nodes --tag stage:check: stdout %q kept nodes that do not carry the tag", out)
	}
}

// @concept: node
func TestInstanceNodesSelectsByTagPrefix(t *testing.T) {
	srv := setupClitest(t)
	id := instanceWithTaggedNodes(t, srv)

	out := captureStdout(t, func() {
		if code := cli.RunInstanceNodes(context.Background(),
			[]string{"--tag-prefix", "stage:", "-o", "json", id}); code != 0 {
			t.Fatalf("instance nodes --tag-prefix: exit %d", code)
		}
	})
	var nodes []cli.Node
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("instance nodes -o json did not emit JSON on stdout: %v (%q)", err, out)
	}
	got := map[string]bool{}
	for _, n := range nodes {
		got[n.ID] = true
	}
	if !got["extract"] || !got["verify"] {
		t.Errorf("instance nodes --tag-prefix stage:: got %v, want every node carrying a stage: tag", got)
	}
	if got["publish"] {
		t.Errorf("instance nodes --tag-prefix stage:: kept the untagged node %v", got)
	}
}
