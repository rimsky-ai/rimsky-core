// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func TestRunTagCreate_OK(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "", "")
	if got := cli.RunTagCreate(context.Background(), []string{"--template", hash, "v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTagCreate_FlagAfterPositional(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "", "")
	if got := cli.RunTagCreate(context.Background(), []string{"v1", "--template", hash}); got != 0 {
		t.Errorf("space form: exit %d, want 0", got)
	}
	if got := cli.RunTagCreate(context.Background(), []string{"v2", "--template=" + hash}); got != 0 {
		t.Errorf("equals form: exit %d, want 0", got)
	}
}

func TestRunTagCreate_RejectComposePrefix(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunTagCreate(context.Background(), []string{"--template", "foo", "compose:p:x"}); got != 2 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTagList(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	srv.State.SetTagHash("v2", hash)
	if got := cli.RunTagList(context.Background(), []string{"--prefix", "v"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTagGet_NotFound(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunTagGet(context.Background(), []string{"missing"}); got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTagGet_Found(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	_ = hash
	if got := cli.RunTagGet(context.Background(), []string{"v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTagMv_OK(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	hash2, _ := srv.State.RegisterTemplate(map[string]any{"name": "y", "version": "1.0", "nodes": []any{}}, "", "")
	if got := cli.RunTagMv(context.Background(), []string{"--template", hash2, "v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	_ = hash
}

func TestRunTagRm_OK(t *testing.T) {
	srv := setupClitest(t)
	srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	if got := cli.RunTagRm(context.Background(), []string{"v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTagMv_RejectComposePrefix(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunTagMv(context.Background(), []string{"--template", "foo", "compose:p:x"}); got != 2 {
		t.Errorf("exit %d, want 2 (compose-owned tag must not be movable through manual CLI)", got)
	}
}

func TestRunTagRm_RejectComposePrefix(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunTagRm(context.Background(), []string{"compose:p:x"}); got != 2 {
		t.Errorf("exit %d, want 2 (compose-owned tag must not be deletable through manual CLI)", got)
	}
}
