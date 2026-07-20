// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestTemplateIDTargets_LogsWarningOnPersistenceError(t *testing.T) {
	h := newUnseededAuthTestHarness(t)
	logger := shared.NewCapturingLogger()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hash := "sha256-" + strings.Repeat("a", 64)
	targets := templateIDTargets(ctx, h.tables, logger, hash)

	if len(targets) != 1 || len(targets[0]) != 0 {
		t.Fatalf("targets: got %v want a single empty (fail-closed) target", targets)
	}

	found := false
	for _, rec := range logger.Records() {
		if rec.Level == "warn" && rec.Msg == "auth.template_targets_lookup_failed" {
			found = true
			if rec.Fields["template_id"] != hash {
				t.Fatalf("log field template_id: got %v want %q", rec.Fields["template_id"], hash)
			}
		}
	}
	if !found {
		t.Fatalf("expected a warn-level auth.template_targets_lookup_failed log record on persistence error, got %+v", logger.Records())
	}
}

func TestTemplateIDTargets_NoRowsDoesNotLog(t *testing.T) {
	h := newUnseededAuthTestHarness(t)
	logger := shared.NewCapturingLogger()

	hash := "sha256-" + strings.Repeat("b", 64)
	targets := templateIDTargets(context.Background(), h.tables, logger, hash)

	if len(targets) != 1 || len(targets[0]) != 0 {
		t.Fatalf("targets: got %v want a single empty target", targets)
	}
	for _, rec := range logger.Records() {
		if rec.Msg == "auth.template_targets_lookup_failed" {
			t.Fatalf("no-rows case must not be logged as a persistence error: %+v", rec)
		}
	}
}
