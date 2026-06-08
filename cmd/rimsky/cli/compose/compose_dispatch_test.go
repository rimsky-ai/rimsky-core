// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_dispatch_test.go — asserts the `compose <verb>` dispatcher
// exists and routes. `cmd/rimsky/main.go`'s top-level `compose` case
// (CLICTRL-5.4) is a one-line delegation to compose.Dispatch; the
// acceptance gate exercises the real binary path, while these unit tests
// pin Dispatch's routing contract (unknown subcommand / no args → exit 2).
package compose_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
)

func TestComposeDispatch_UnknownSubcommand(t *testing.T) {
	if got := compose.Dispatch(context.Background(), []string{"bogus"}); got != 2 {
		t.Errorf("Dispatch(bogus) = %d, want 2", got)
	}
}

func TestComposeDispatch_NoArgs(t *testing.T) {
	if got := compose.Dispatch(context.Background(), nil); got != 2 {
		t.Errorf("Dispatch(nil) = %d, want 2", got)
	}
}
