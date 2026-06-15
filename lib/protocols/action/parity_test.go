// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package action

import "testing"

// TestSharedVocab_FsAndPgUseSameNames is a single-source-of-truth
// assertion: the fs-store and pg-store both import action.Pop,
// action.Recycle, etc. and rely on the constants here. This test
// pins the string values so a rename here can't silently diverge
// from existing test fixtures and operator-facing YAML.
func TestSharedVocab_FsAndPgUseSameNames(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{Pop, "pop"},
		{PopAndMove, "pop_and_move"},
		{PopAndDelete, "pop_and_delete"},
		{Recycle, "recycle"},
	}
	for _, c := range cases {
		if string(c.kind) != c.want {
			t.Errorf("Kind %v: string value %q, want %q", c.kind, c.kind, c.want)
		}
	}
	// @constraint: AllKinds must enumerate exactly four entries — the count
	// is part of the cross-store action-vocabulary contract.
	if got := len(AllKinds()); got != 4 {
		t.Errorf("AllKinds returned %d kinds; want 4", got)
	}
}
