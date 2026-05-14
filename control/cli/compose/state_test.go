// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package compose_test

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/control/cli"
	"github.com/fallguy/rimsky/control/cli/compose"
	"github.com/fallguy/rimsky/control/cli/internal/clitest"
)

func TestQueryState_FiltersByPrefix(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "compose:p:foo", "")
	srv.State.SetTemplateState(hash, "deployed")
	// Tag for another project should be filtered out.
	srv.State.SetTagHash("compose:other:foo", hash)
	// Manual tag (no compose: prefix) also filtered out.
	srv.State.SetTagHash("manual-foo", hash)
	// One owned instance.
	key := "compose:p:hello"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}
	// One foreign instance.
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
