// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
