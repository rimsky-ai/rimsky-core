// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package clitest_test

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func TestInMemoryState_CreateInstance_NoIDCollisionAfterDelete(t *testing.T) {
	s := clitest.NewInMemoryState()
	hash, _ := s.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")

	first, _, err := s.CreateInstance(hash, nil, nil)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, _, err := s.CreateInstance(hash, nil, nil)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("first and second instance got the same id %q", first.ID)
	}

	s.DeleteInstance(first.ID)

	third, _, err := s.CreateInstance(hash, nil, nil)
	if err != nil {
		t.Fatalf("create third: %v", err)
	}
	if third.ID == second.ID {
		t.Fatalf("third instance id %q collided with still-live second instance %q after deleting first", third.ID, second.ID)
	}

	if got := s.FindInstance(second.ID); got == nil || got.ID != second.ID {
		t.Fatalf("second instance should still be findable and untouched after the collision, got %+v", got)
	}
}
