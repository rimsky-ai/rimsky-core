// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"strings"
	"testing"
)

func TestDispatchTemplate_RegisterAndRmRouteToTheirOwnHandlers(t *testing.T) {
	for _, verb := range []string{"register", "rm"} {
		code, out := runCapt(t, dispatchTemplate, []string{verb})
		if code != 2 {
			t.Errorf("dispatchTemplate %q with no args: exit %d, want 2", verb, code)
		}
		if strings.Contains(out, "unknown subcommand") {
			t.Errorf("dispatchTemplate %q: fell through to the unknown-subcommand branch instead of "+
				"routing to its own handler; stderr=%q", verb, out)
		}
		if !strings.Contains(out, "usage: rimsky template "+verb) {
			t.Errorf("dispatchTemplate %q: expected the verb's own usage message, got stderr=%q", verb, out)
		}
	}
}

func TestDispatchTag_MvAndRmRouteToTheirOwnHandlers(t *testing.T) {
	for _, verb := range []string{"mv", "rm"} {
		code, out := runCapt(t, dispatchTag, []string{verb})
		if code != 2 {
			t.Errorf("dispatchTag %q with no args: exit %d, want 2", verb, code)
		}
		if strings.Contains(out, "unknown subcommand") {
			t.Errorf("dispatchTag %q: fell through to the unknown-subcommand branch instead of "+
				"routing to its own handler; stderr=%q", verb, out)
		}
		if !strings.Contains(out, "usage: rimsky tag "+verb) {
			t.Errorf("dispatchTag %q: expected the verb's own usage message, got stderr=%q", verb, out)
		}
	}
}
