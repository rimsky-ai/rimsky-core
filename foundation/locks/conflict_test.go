package locks

import "testing"

// TestScopesByteEqual covers the rimsky-side byte-equal scope conflict
// predicate (per v3 spec §7.7). Empty scopes never conflict; identical
// bytes conflict; different bytes do not.
func TestScopesByteEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{"both empty", nil, nil, false},
		{"a empty", nil, []byte("x"), false},
		{"b empty", []byte("x"), nil, false},
		{"identical short", []byte("ab"), []byte("ab"), true},
		{"identical long", []byte("abcdefghijklmnop"), []byte("abcdefghijklmnop"), true},
		{"different length", []byte("ab"), []byte("abc"), false},
		{"different bytes", []byte("ab"), []byte("ac"), false},
		{"identical json bytes", []byte(`{"item":"alpha"}`), []byte(`{"item":"alpha"}`), true},
		{"different json bytes", []byte(`{"item":"alpha"}`), []byte(`{"item":"beta"}`), false},
		{"empty slice vs empty slice", []byte{}, []byte{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScopesByteEqual(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("ScopesByteEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestModeCoexistsMatrix exhaustively walks the C3.1 matrix per v3 spec
// §4.10 invariant 4b / spec §8.5 carry-forward. Sync block: r×r ✓; r×w /
// w×r / w×w ✗. Async block: r×r / r×w / w×r ✓; w×w ✗.
func TestModeCoexistsMatrix(t *testing.T) {
	// Helper: every sync write_semantics value should behave the same
	// way; assert both Direct and StagedBlocking pairings.
	syncSemantics := []WriteSemantics{WriteSemanticsSync, WriteSemanticsBlockingAsync}
	for _, sem := range syncSemantics {
		t.Run("sync-"+string(sem)+"-r-r", func(t *testing.T) {
			if !ModeCoexists(IntentRead, sem, IntentRead, sem) {
				t.Fatal("sync r×r must coexist")
			}
		})
		t.Run("sync-"+string(sem)+"-r-w", func(t *testing.T) {
			if ModeCoexists(IntentRead, sem, IntentReadWrite, sem) {
				t.Fatal("sync r×w must conflict")
			}
		})
		t.Run("sync-"+string(sem)+"-w-r", func(t *testing.T) {
			if ModeCoexists(IntentReadWrite, sem, IntentRead, sem) {
				t.Fatal("sync w×r must conflict")
			}
		})
		t.Run("sync-"+string(sem)+"-w-w", func(t *testing.T) {
			if ModeCoexists(IntentReadWrite, sem, IntentReadWrite, sem) {
				t.Fatal("sync w×w must conflict")
			}
		})
	}

	// Async block (staged_async).
	async := WriteSemanticsStagedAsync
	t.Run("async-r-r", func(t *testing.T) {
		if !ModeCoexists(IntentRead, async, IntentRead, async) {
			t.Fatal("async r×r must coexist")
		}
	})
	t.Run("async-r-w", func(t *testing.T) {
		if !ModeCoexists(IntentRead, async, IntentReadWrite, async) {
			t.Fatal("async r×w must coexist")
		}
	})
	t.Run("async-w-r", func(t *testing.T) {
		if !ModeCoexists(IntentReadWrite, async, IntentRead, async) {
			t.Fatal("async w×r must coexist")
		}
	})
	t.Run("async-w-w", func(t *testing.T) {
		if ModeCoexists(IntentReadWrite, async, IntentReadWrite, async) {
			t.Fatal("async w×w must conflict")
		}
	})
}

// TestModeCoexistsCrossQuadrant exercises the unreachable-but-defensive
// cross-block path. The helper returns true (no conflict) when one
// claim is sync and the other is async — the upstream filter normally
// prevents this combo because two claims on the same store share its
// write_semantics.
func TestModeCoexistsCrossQuadrant(t *testing.T) {
	if !ModeCoexists(IntentRead, WriteSemanticsSync, IntentRead, WriteSemanticsStagedAsync) {
		t.Fatal("cross-quadrant r×r should report no conflict")
	}
	if !ModeCoexists(IntentReadWrite, WriteSemanticsSync, IntentReadWrite, WriteSemanticsStagedAsync) {
		t.Fatal("cross-quadrant w×w should report no conflict (different semantics)")
	}
}

// TestModeCoexistsSymmetric: the matrix is symmetric in both arguments.
// Verify exhaustively across all (intent×semantics)² combinations.
func TestModeCoexistsSymmetric(t *testing.T) {
	intents := []Intent{IntentRead, IntentReadWrite}
	semantics := []WriteSemantics{WriteSemanticsSync, WriteSemanticsBlockingAsync, WriteSemanticsStagedAsync}
	for _, ia := range intents {
		for _, sa := range semantics {
			for _, ib := range intents {
				for _, sb := range semantics {
					ab := ModeCoexists(ia, sa, ib, sb)
					ba := ModeCoexists(ib, sb, ia, sa)
					if ab != ba {
						t.Fatalf("ModeCoexists not symmetric for (%v,%v,%v,%v): ab=%v ba=%v", ia, sa, ib, sb, ab, ba)
					}
				}
			}
		}
	}
}
