// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"strings"
	"testing"

	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

func TestEmitSignalInTx_RejectsNonCanonicalTypePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sig  signalpkg.Signal
	}{
		{
			name: "empty type-path",
			sig:  signalpkg.Signal{Type: ""},
		},
		{
			name: "unknown top-level kind",
			sig:  signalpkg.Signal{Type: signalpkg.TypePath("lifecycle/node_created")},
		},
		{
			name: "retired message kind",
			sig:  signalpkg.Signal{Type: signalpkg.TypePath("message/invalidate/operator/self")},
		},
		{
			name: "malformed attribute leaf",
			sig:  signalpkg.Signal{Type: signalpkg.TypePath("attribute/changed")},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := emitSignalInTxOnce(
				context.Background(),
				RunArgs{},
				nil,
				foundationshared.UUID{},
				"",
				foundationshared.UUID{},
				foundationshared.UUID{},
				foundationshared.UUID{},
				c.sig,
			)
			if err == nil {
				t.Fatalf("expected non-canonical type-path %q to be rejected", c.sig.Type)
			}
			if !strings.Contains(err.Error(), "not in canonical taxonomy") &&
				!strings.Contains(err.Error(), "empty") {
				t.Fatalf("expected taxonomy-rejection error, got: %v", err)
			}
		})
	}
}

func TestEmitSignalInTx_AcceptsCanonicalTransientInfraAndReleaseAndRequeue(t *testing.T) {
	t.Parallel()
	cases := []signalpkg.TypePath{
		"transient/infra/heartbeat_lost",
		"transient/release_and_requeue/lock_lost",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			t.Parallel()
			if err := signalpkg.ValidateTypePath(c); err != nil {
				t.Fatalf("ValidateTypePath(%q) returned %v; expected nil — these patterns must remain in canonicalEmitPatterns since the runtime emits them at runner_error_policy.go", c, err)
			}
		})
	}
}
