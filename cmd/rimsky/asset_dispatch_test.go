// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"strings"
	"testing"
)

func TestDispatchAsset_ListShowVersionsDeleteLineageRouteToTheirOwnHandlers(t *testing.T) {
	for _, verb := range []string{"list", "show", "versions", "delete", "lineage"} {
		code, out := runCapt(t, dispatchAsset, []string{verb, "--endpoint", "http://unused.invalid"})
		if code != 2 {
			t.Errorf("dispatchAsset %q with no args: exit %d, want 2", verb, code)
		}
		if strings.Contains(out, "unknown subcommand") {
			t.Errorf("dispatchAsset %q: fell through to the unknown-subcommand branch instead of "+
				"routing to its own handler; stderr=%q", verb, out)
		}
		if !strings.Contains(out, "usage: rimsky asset "+verb) {
			t.Errorf("dispatchAsset %q: expected the verb's own usage message, got stderr=%q", verb, out)
		}
	}
}

func TestDispatchAsset_MaterializeIsNotWired(t *testing.T) {
	code, out := runCapt(t, dispatchAsset, []string{"materialize", "--endpoint", "http://unused.invalid"})
	if code != 2 {
		t.Errorf("dispatchAsset materialize: exit %d, want 2", code)
	}
	if !strings.Contains(out, `unknown subcommand "materialize"`) {
		t.Errorf("dispatchAsset materialize: expected the unknown-subcommand branch "+
			"(materialize was retired 2026-06-17 and must never be re-wired); stderr=%q", out)
	}
}
