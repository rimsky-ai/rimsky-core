// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"strings"
	"testing"
)

var notTheVerbsOwnRendering = map[string]bool{
	"cli.runWithCommon":         true,
	"cli.RegisterCommonFlags":   true,
	"cli.reportError":           true,
	"cli.ReportDryRunPreview":   true,
	"compose.reportApplyError":  true,
	"compose.reportPlanError":   true,
	"compose.parseComposeFlags": true,
}

var outputFormatExceptions = map[string]string{}

// @concept: rimsky
func TestEveryVerbDeclaringTheOutputFormatFlagReadsIt(t *testing.T) {
	g := loadCLICallGraph(t, findRepoRoot(t))

	for name := range outputFormatExceptions {
		if g.funcs[name] == nil {
			t.Errorf("the exception list names %s, which no longer exists: an exception that covers nothing "+
				"hides the verb it was written for", name)
		}
	}

	var declaring, exempt, formatless []string
	for _, verb := range g.verbs {
		tokens := g.reach(verb)
		if !anyToken(tokens, "runWithCommon", "RegisterCommonFlags") {
			formatless = append(formatless, verb)
			continue
		}
		if _, ok := outputFormatExceptions[verb]; ok {
			exempt = append(exempt, verb)
			continue
		}
		declaring = append(declaring, verb)
		idents := g.reachIdents(verb, notTheVerbsOwnRendering)
		if !idents["FormatJSON"] {
			t.Errorf("%s registers the common flag set, so its help advertises -o json. It never reads the "+
				"resolved format. A verb that declares the flag renders what the flag asks for", verb)
		}
	}

	if len(declaring) == 0 {
		t.Fatalf("this check found no verb registering the common flag set across %d verbs, so it inspected "+
			"nothing", len(g.verbs))
	}
	t.Logf("checked %d verbs declaring the output-format flag: %s", len(declaring), strings.Join(declaring, ", "))
	t.Logf("%d verbs declare no output-format flag: %s", len(formatless), strings.Join(formatless, ", "))
	t.Logf("%d verbs stand outside the rule by exception: %s", len(exempt), strings.Join(exempt, ", "))
}
