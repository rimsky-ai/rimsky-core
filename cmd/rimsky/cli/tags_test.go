// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"strings"
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
	var got int
	out := captureStdout(t, func() {
		got = cli.RunTagGet(context.Background(), []string{"v1"})
	})
	if got != 0 {
		t.Errorf("exit %d", got)
	}
	if !strings.Contains(out, "template_hash:") {
		t.Errorf("tag get: stdout must display the template_hash key (CLI vocab), got %q", out)
	}
	if !strings.Contains(out, hash) {
		t.Errorf("tag get: stdout must carry the resolved template hash %q, got %q", hash, out)
	}
	if strings.Contains(out, "template_id") {
		t.Errorf("tag get: stdout must not use the retired template_id display key, got %q", out)
	}
}

func TestRunTagMv_OK(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	hash2, _ := srv.State.RegisterTemplate(map[string]any{"name": "y", "version": "1.0", "nodes": []any{}}, "", "")
	var got int
	out := captureStdout(t, func() {
		got = cli.RunTagMv(context.Background(), []string{"--template", hash2, "v1"})
	})
	if got != 0 {
		t.Errorf("exit %d", got)
	}
	if !strings.Contains(out, "v1") || !strings.Contains(out, hash2) {
		t.Errorf("tag mv: stdout must display both the moved tag and the target ref, got %q", out)
	}
	if !strings.Contains(out, "→") {
		t.Errorf("tag mv: stdout must display the tag-to-ref arrow, got %q", out)
	}
	_ = hash
}

func TestRunTagRm_OK(t *testing.T) {
	srv := setupClitest(t)
	srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	var got int
	out := captureStdout(t, func() {
		got = cli.RunTagRm(context.Background(), []string{"v1"})
	})
	if got != 0 {
		t.Errorf("exit %d", got)
	}
	if !strings.Contains(out, "v1 removed") {
		t.Errorf("tag rm: stdout must confirm the removed tag, got %q", out)
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
