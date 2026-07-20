// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose_test

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
)

func TestRunComposeDown_Terminal(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	insts := srv.State.ListInstances("", "")
	if len(insts) != 1 {
		t.Fatalf("got %+v", insts)
	}
	now := time.Now()
	srv.State.SetInstanceTerminated(insts[0].ID, &now)
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	if len(srv.State.ListInstances("", "")) != 0 {
		t.Errorf("instances remain")
	}
}

func TestRunComposeDown_RejectsStrayPositionalArgs(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes", "bogus"}); got != 2 {
		t.Fatalf("exit %d, want 2 for a stray positional argument", got)
	}
}

func TestRunComposeDown_NonTerminalRefuses(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	_ = srv
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes"}); got != 1 {
		t.Errorf("exit %d, want 1", got)
	}
}
