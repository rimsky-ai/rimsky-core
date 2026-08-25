// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func instanceSpanningNodePages(t *testing.T, srv *clitest.Server, failedNodeIndex int) string {
	t.Helper()
	srv.ListNodesDefaultPageSize = 2
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	for i := 0; i < 5; i++ {
		summary := &cli.NodeRunSummary{FreshCount: 1}
		if i == failedNodeIndex {
			summary = &cli.NodeRunSummary{FailedCount: 1}
		}
		srv.State.AddNode(inst.ID, cli.Node{
			ID:         fmt.Sprintf("node-%d", i),
			InstanceID: inst.ID,
			NodeType:   fmt.Sprintf("worker-%d", i),
			RunSummary: summary,
		})
	}
	return inst.ID
}

// @concept: node
func TestInstanceNodesListsAnInstanceThatSpansSeveralPages(t *testing.T) {
	srv := setupClitest(t)
	id := instanceSpanningNodePages(t, srv, -1)

	out := captureStdout(t, func() {
		if code := cli.RunInstanceNodes(context.Background(), []string{"-o", "json", id}); code != 0 {
			t.Fatalf("instance nodes: exit %d", code)
		}
	})
	var nodes []cli.Node
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(nodes) != 5 {
		t.Fatalf("instance nodes listed %d of 5 nodes; a page boundary must not truncate the listing", len(nodes))
	}
}

// @decision: exit-codes
// @story: script-friendly-outcome
func TestTheRunOutcomeSeesAFailedNodeBeyondTheFirstPage(t *testing.T) {
	srv := setupClitest(t)
	id := instanceSpanningNodePages(t, srv, 4)

	c := cli.NewClient(srv.URL)
	nodes, err := cli.PagedListInstanceNodes(context.Background(), c, id, cli.ListNodesQuery{})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	outcome, count := cli.ClassifyInstanceOutcome(nodes)
	if count != 5 {
		t.Fatalf("classified %d of 5 nodes", count)
	}
	if outcome != cli.OutcomeFailure {
		t.Fatalf("a node that failed on the last page decides the outcome; got %q", outcome)
	}
}

func nodePageServer(t *testing.T, handle func(calls int) (rows []cli.Node, next string)) *httptest.Server {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 10 {
			http.Error(w, `{"error":"the client never stopped asking for pages"}`, http.StatusInternalServerError)
			return
		}
		rows, next := handle(calls)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"nodes": rows, "next_cursor": next})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTheNodeCursorWalkStopsOnACursorTheServerRepeats(t *testing.T) {
	srv := nodePageServer(t, func(calls int) ([]cli.Node, string) {
		return []cli.Node{{ID: fmt.Sprintf("node-%d", calls)}}, "same-cursor"
	})
	nodes, err := cli.PagedListInstanceNodes(context.Background(), cli.NewClient(srv.URL), "i", cli.ListNodesQuery{})
	if err != nil {
		t.Fatalf("the walk must stop rather than ask forever: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("the walk read %d pages before stopping on the repeated cursor; want 2", len(nodes))
	}
}

func TestTheNodeCursorWalkStopsOnAPageWithNoRows(t *testing.T) {
	srv := nodePageServer(t, func(calls int) ([]cli.Node, string) {
		if calls == 1 {
			return []cli.Node{{ID: "node-1"}}, "cursor-1"
		}
		return nil, fmt.Sprintf("cursor-%d", calls)
	})
	nodes, err := cli.PagedListInstanceNodes(context.Background(), cli.NewClient(srv.URL), "i", cli.ListNodesQuery{})
	if err != nil {
		t.Fatalf("the walk must stop rather than ask forever: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("the walk collected %d nodes; a page with no rows ends the walk", len(nodes))
	}
}

func TestBreakpointHitsWalkTheirCursorToTheWholeSet(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	const hits = 5
	for i := 0; i < hits; i++ {
		srv.State.AddBreakpointHit(inst.ID, map[string]any{"checkpoint": "pre_dispatch", "mode": "stop"})
	}

	c := cli.NewClient(srv.URL)
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages <= hits; pages++ {
		page, err := c.ListBreakpointHits(context.Background(), inst.ID, 2, cursor)
		if err != nil {
			t.Fatalf("ListBreakpointHits: %v", err)
		}
		for _, h := range page.Hits {
			id, _ := h["hit_id"].(string)
			if seen[id] {
				t.Fatalf("hit %q came back on two pages", id)
			}
			seen[id] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != hits {
		t.Fatalf("the cursor walk reached %d of %d hits", len(seen), hits)
	}
}

func TestBreakpointHitsRefuseAMalformedCursor(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	c := cli.NewClient(srv.URL)
	if _, err := c.ListBreakpointHits(context.Background(), inst.ID, 2, "not-a-cursor"); err == nil {
		t.Fatal("a cursor the server did not mint must be refused, not read as the first page")
	}
}
