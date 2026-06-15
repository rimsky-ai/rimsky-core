// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func TestQueryState_FiltersByPrefix(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "compose:p:foo", "")
	srv.State.SetTemplateState(hash, "deployed")
	srv.State.SetTagHash("compose:other:foo", hash)
	srv.State.SetTagHash("manual-foo", hash)
	key := "compose:p:hello"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}
	other := "compose:other:bar"
	if _, _, err := srv.State.CreateInstance(hash, &other, nil); err != nil {
		t.Fatal(err)
	}

	c := cli.NewClient(srv.URL)
	state, err := compose.QueryState(context.Background(), c, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tags) != 1 || state.Tags[0].Tag != "compose:p:foo" {
		t.Errorf("tags: %+v", state.Tags)
	}
	if _, ok := state.TemplatesByH[hash]; !ok {
		t.Errorf("template missing: %+v", state.TemplatesByH)
	}
	if len(state.Instances) != 1 {
		t.Errorf("instances: %+v", state.Instances)
	}
	if state.Instances[0].InstanceKey == nil || *state.Instances[0].InstanceKey != key {
		t.Errorf("wrong instance: %+v", state.Instances[0])
	}
}
