// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package action

import "testing"

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
	if got := len(AllKinds()); got != 4 {
		t.Errorf("AllKinds returned %d kinds; want 4", got)
	}
}
