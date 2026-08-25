// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

type destructiveVerb struct {
	name   string
	run    func(context.Context, []string) int
	args   []string
	target string
}

func destructiveVerbs(endpoint string) []destructiveVerb {
	return []destructiveVerb{
		{"tag rm", cli.RunTagRm, []string{"--endpoint", endpoint, "release"}, "release"},
		{"instance delete", cli.RunInstanceDelete, []string{"--endpoint", endpoint, "inst-1"}, "inst-1"},
		{"instance kill", cli.RunInstanceKill, []string{"--endpoint", endpoint, "--force", "inst-1"}, "inst-1"},
		{"template undeploy", cli.RunTemplateUndeploy, []string{"--endpoint", endpoint, "release"}, "release"},
		{"template rm", cli.RunTemplateRm, []string{"--endpoint", endpoint, "release"}, "release"},
		{"auth revoke", cli.RunAuthRevoke, []string{"--endpoint", endpoint, "deployer"}, "deployer"},
		{"admin reset", cli.RunAdminReset, []string{"--endpoint", endpoint, "node-1"}, "node-1"},
		{"lineage prune", cli.RunLineagePrune, []string{"--endpoint", endpoint, "--older-than", "24h"}, "lineage"},
		{"asset delete", cli.RunAssetDelete, []string{"--endpoint", endpoint, "--instance", "inst-1", "report"}, "report"},
	}
}

func TestEveryDestructiveVerbRefusesWithoutATerminalUntilTheOperatorConfirms(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, verb := range destructiveVerbs(srv.URL) {
		t.Run(verb.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("RIMSKY_CONTEXT", "")
			t.Setenv("RIMSKY_API_KEY", "k")
			before := requests.Load()

			var code int
			complaint := captureStderr(t, func() {
				code = verb.run(context.Background(), verb.args)
			})

			if code != 2 {
				t.Errorf("rimsky %s with no terminal and no --yes: exit %d, want 2. A destructive verb "+
					"that cannot ask refuses", verb.name, code)
			}
			if got := requests.Load(); got != before {
				t.Errorf("rimsky %s sent %d request(s) before confirming; a refused operation sends none",
					verb.name, got-before)
			}
			if !strings.Contains(complaint, "--yes") || !strings.Contains(complaint, verb.target) {
				t.Errorf("rimsky %s refusal said %q, want the target %q and the flag that confirms it",
					verb.name, complaint, verb.target)
			}
		})
	}
}

func TestConfirmedDestructiveVerbProceedsAgainstTheDeployment(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "release")

	if code := cli.RunTagRm(context.Background(), []string{"--yes", "release"}); code != 0 {
		t.Fatalf("tag rm --yes: exit %d, want 0", code)
	}
	for _, tag := range srv.State.ListTags() {
		if tag.Tag == "release" {
			t.Errorf("tag rm --yes left the tag in place: %+v", tag)
		}
	}
	if _, ok := srv.State.GetTemplate(hash); !ok {
		t.Errorf("tag rm removed the template it named; only the tag is the target")
	}
}

func TestDestructiveConfirmationProceedsOnlyOnAffirmativeAnswer(t *testing.T) {
	for _, answer := range []struct {
		typed    string
		proceeds bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"\n", false},
		{"yes please\n", false},
	} {
		var out strings.Builder
		got := cli.ConfirmDestructive(false, true, strings.NewReader(answer.typed), &out,
			[]string{"remove tag release"})
		if got != answer.proceeds {
			t.Errorf("answering %q: proceeds=%v, want %v", answer.typed, got, answer.proceeds)
		}
		if !strings.Contains(out.String(), "Proceed? [y/N]") {
			t.Errorf("answering %q: prompt %q never asked the question", answer.typed, out.String())
		}
		if !strings.Contains(out.String(), "remove tag release") {
			t.Errorf("answering %q: prompt %q never named the target", answer.typed, out.String())
		}
	}
}

func TestDestructiveConfirmationAsksNothingUnderYes(t *testing.T) {
	var out strings.Builder
	if !cli.ConfirmDestructive(true, true, strings.NewReader(""), &out, []string{"remove tag release"}) {
		t.Error("--yes must proceed without asking")
	}
	if out.Len() != 0 {
		t.Errorf("--yes wrote %q; the flag answers the question, so the verb asks none", out.String())
	}
}
