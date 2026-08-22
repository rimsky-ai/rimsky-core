// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
)

func TestRunRun_Keep(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := compose.RunTemplateRun(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

// @story: one-shot-to-terminal
// @decision: termination
func TestRunRun_NoKeep(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)

	if got := compose.RunTemplateRun(context.Background(), []string{"--no-keep", "--poll-interval", "20ms", specPath}); got != 0 {
		t.Errorf("exit %d: the run terminates its own instance once the instance is quiescent, "+
			"so nothing outside the verb has to stamp it terminal", got)
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

const rootlessSpec = `name: rootless
version: "1.0"
messages:
  - type: ping/recheck
    body_schema:
      type: object
      properties:
        reason:
          type: string
nodes:
  - type: receiver
    executor: http-node
    attributes:
      schema:
        type: object
        properties:
          url:
            type: string
            source: "{{messages.ping/recheck.reason}}"
`

// @story: one-shot-to-terminal
// @decision: termination
// @decision: compose-driver-sends-empty-message-after-create
func TestRunRunRemote_RefusesATemplateNothingWillDrive(t *testing.T) {
	srv := setupClitest(t)
	specPath := writeSpecContent(t, "rootless.yml", rootlessSpec)

	var code int
	stderr := captureStderr(t, func() {
		code = cli.RunRunRemote(context.Background(), &cli.CommonFlags{}, srv.URL, cli.RunFlags{
			TemplateFile: specPath,
			PollInterval: 10 * time.Millisecond,
		})
	})

	if code != 2 {
		t.Fatalf("exit %d, want 2: a run whose template declares no structural root sends no wake message, "+
			"so nothing drives the instance and the run must refuse instead of reporting success", code)
	}
	if !strings.Contains(stderr, "no structural root") {
		t.Errorf("stderr = %q, want it to name the missing structural root", stderr)
	}
	if got := len(srv.State.ListInstances("", "")); got != 0 {
		t.Errorf("the refused run left %d instances behind; it refuses before it creates one", got)
	}
}

// @story: one-shot-to-terminal
func TestRunRunRemote_KeepsARootlessTemplateForTheOperatorToDrive(t *testing.T) {
	srv := setupClitest(t)
	specPath := writeSpecContent(t, "rootless.yml", rootlessSpec)

	code := cli.RunRunRemote(context.Background(), &cli.CommonFlags{}, srv.URL, cli.RunFlags{
		TemplateFile: specPath,
		Keep:         true,
	})

	if code != 0 {
		t.Fatalf("exit %d, want 0: --keep hands the instance to the operator, who drives it, so the "+
			"structural root the run itself needs is not required", code)
	}
	if got := len(srv.State.ListInstances("", "")); got != 1 {
		t.Errorf("the kept run left %d instances, want 1", got)
	}
}
