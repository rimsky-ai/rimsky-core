// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func TestRunRun_Keep(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := compose.RunTemplateRun(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunRun_NoKeep(t *testing.T) {
	srv := setupClitest(t)
	specPath := writeSpec(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		awaited.Until(t, "an active instance the stub can mark terminated", func() bool {
			return srv.State.MarkFirstActiveTerminated() != ""
		})
	}()
	defer func() { <-done }()

	if got := compose.RunTemplateRun(context.Background(), []string{"--no-keep", "--poll-interval", "20ms", specPath}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunLs_DefaultsToInstances(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunLs(context.Background(), nil); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunLs_Templates(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunLs(context.Background(), []string{"templates"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunLs_Tags(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunLs(context.Background(), []string{"tags"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}
