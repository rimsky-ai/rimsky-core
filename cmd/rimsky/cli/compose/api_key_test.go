// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

const composeTestKey = "rk_compose_operator"

func composeVerbs() []struct {
	name string
	run  func(context.Context, []string) int
	args []string
} {
	return []struct {
		name string
		run  func(context.Context, []string) int
		args []string
	}{
		{name: "up", run: compose.RunComposeUp, args: []string{"--yes"}},
		{name: "plan", run: compose.RunComposePlan},
		{name: "status", run: compose.RunComposeStatus},
		{name: "down", run: compose.RunComposeDown, args: []string{"--yes"}},
	}
}

func assertEveryRequestCarried(t *testing.T, srv *clitest.Server, key string) {
	t.Helper()
	bearers := srv.SeenBearers()
	if len(bearers) == 0 {
		t.Fatalf("the verb sent the control API no request, so this check proved nothing")
	}
	for i, presented := range bearers {
		if presented != key {
			t.Errorf("request %d carried bearer %q, want %q", i, presented, key)
		}
	}
	if n := srv.UnauthorizedCount(); n != 0 {
		t.Errorf("the control API refused %d request(s) as unauthorized", n)
	}
}

// @concept: rimsky
func TestComposeVerbsSendTheKeyGivenOnTheFlag(t *testing.T) {
	for _, verb := range composeVerbs() {
		t.Run(verb.name, func(t *testing.T) {
			srv := setupServer(t)
			srv.RequireBearer(composeTestKey)
			mf := writeFullManifest(t)
			args := append([]string{"-f", mf, "--key", composeTestKey}, verb.args...)
			if code := verb.run(context.Background(), args); code == 2 {
				t.Fatalf("compose %s exited 2, so it failed before reaching the control API", verb.name)
			}
			assertEveryRequestCarried(t, srv, composeTestKey)
		})
	}
}

// @concept: rimsky
func TestComposeVerbsFallBackToTheKeyInTheEnvironment(t *testing.T) {
	for _, verb := range composeVerbs() {
		t.Run(verb.name, func(t *testing.T) {
			srv := setupServer(t)
			srv.RequireBearer(composeTestKey)
			t.Setenv("RIMSKY_API_KEY", composeTestKey)
			mf := writeFullManifest(t)
			args := append([]string{"-f", mf}, verb.args...)
			if code := verb.run(context.Background(), args); code == 2 {
				t.Fatalf("compose %s exited 2, so it failed before reaching the control API", verb.name)
			}
			assertEveryRequestCarried(t, srv, composeTestKey)
		})
	}
}

// @concept: rimsky
func TestComposeVerbsSendNoKeyWhenNoneResolves(t *testing.T) {
	srv := setupServer(t)
	srv.RequireBearer(composeTestKey)
	mf := writeFullManifest(t)

	if code := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); code == 0 {
		t.Fatalf("compose up succeeded against a control API demanding a bearer token it never sent")
	}
	if srv.UnauthorizedCount() == 0 {
		t.Errorf("the control API refused nothing, so the verb never reached the gate")
	}
}
