// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"context"
	"fmt"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func TestPrecomputeUndeployBindings_SeesInstanceBeyondFirstPage(t *testing.T) {
	srv := clitest.NewServer(t)
	t.Cleanup(srv.Close)
	srv.ListInstancesDefaultPageSize = 1

	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "compose:p:a@1", "")
	srv.State.SetTemplateState(hash, "deployed")

	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("compose:p:owned-%d", i)
		if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
	}
	foreignKey := "not-compose-managed"
	if _, _, err := srv.State.CreateInstance(hash, &foreignKey, nil); err != nil {
		t.Fatalf("CreateInstance (foreign): %v", err)
	}

	c := cli.NewClient(srv.URL)
	plan := &Plan{Steps: []Step{{Action: ActionUndeploy, TemplateHash: hash}}}

	out := precomputeUndeployBindings(context.Background(), c, "p", plan)
	if !out[hash] {
		t.Fatalf("precomputeUndeployBindings = %+v, want hash %q flagged destructive (a non-compose-bound instance beyond page 1 must not be missed)", out, hash)
	}
}

func TestPrecomputeUndeployBindings_AllComposeManagedNotDestructive(t *testing.T) {
	srv := clitest.NewServer(t)
	t.Cleanup(srv.Close)

	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "compose:p:a@1", "")
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:owned-0"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	c := cli.NewClient(srv.URL)
	plan := &Plan{Steps: []Step{{Action: ActionUndeploy, TemplateHash: hash}}}

	out := precomputeUndeployBindings(context.Background(), c, "p", plan)
	if out[hash] {
		t.Fatalf("precomputeUndeployBindings = %+v, want hash %q NOT flagged destructive (all live instances are compose-owned)", out, hash)
	}
}

func TestPrecomputeUndeployBindings_ListInstancesErrorIsConservativelyDestructive(t *testing.T) {
	srv := clitest.NewServer(t)
	t.Cleanup(srv.Close)

	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "compose:p:a@1", "")
	srv.State.SetTemplateState(hash, "deployed")
	srv.SetFailure("GET", "/v1/instances", clitest.FailureSpec{Status: 500, Body: map[string]any{"error": "boom"}, Times: 1})

	c := cli.NewClient(srv.URL)
	plan := &Plan{Steps: []Step{{Action: ActionUndeploy, TemplateHash: hash}}}

	out := precomputeUndeployBindings(context.Background(), c, "p", plan)
	if !out[hash] {
		t.Fatalf("precomputeUndeployBindings = %+v, want hash %q flagged destructive when ListInstances errors (fail conservative)", out, hash)
	}
}

func TestDestructive(t *testing.T) {
	flagged := map[string]bool{"h1": true}
	cases := []struct {
		name string
		step Step
		want bool
	}{
		{"explicit destructive flag wins", Step{Destructive: true, Action: ActionInstanceCreate}, true},
		{"undeploy with non-compose bindings", Step{Action: ActionUndeploy, TemplateHash: "h1"}, true},
		{"undeploy with only compose bindings", Step{Action: ActionUndeploy, TemplateHash: "h2"}, false},
		{"non-undeploy action ignores the bindings map", Step{Action: ActionTagCreate, TemplateHash: "h1"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := destructive(c.step, flagged); got != c.want {
				t.Errorf("destructive(%+v) = %v, want %v", c.step, got, c.want)
			}
		})
	}
}
