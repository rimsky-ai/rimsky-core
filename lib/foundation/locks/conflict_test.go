// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package locks

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestClaimScopesByteEqual(t *testing.T) {
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
			got := ClaimScopesByteEqual(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("ClaimScopesByteEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestModeCoexistsMatrix(t *testing.T) {
	nonPassThrough := []claimproducer.WriteSemantics{
		claimproducer.WriteSemanticsSync,
		claimproducer.WriteSemanticsBlockingAsync,
		claimproducer.WriteSemanticsReadOnly,
	}
	for _, sem := range nonPassThrough {
		t.Run(string(sem)+"-r-r", func(t *testing.T) {
			if !ModeCoexists(claimproducer.IntentRead, claimproducer.IntentRead, sem) {
				t.Fatalf("%s r×r must coexist", sem)
			}
		})
		t.Run(string(sem)+"-r-w", func(t *testing.T) {
			if ModeCoexists(claimproducer.IntentRead, claimproducer.IntentReadWrite, sem) {
				t.Fatalf("%s r×w must conflict", sem)
			}
		})
		t.Run(string(sem)+"-w-r", func(t *testing.T) {
			if ModeCoexists(claimproducer.IntentReadWrite, claimproducer.IntentRead, sem) {
				t.Fatalf("%s w×r must conflict", sem)
			}
		})
		t.Run(string(sem)+"-w-w", func(t *testing.T) {
			if ModeCoexists(claimproducer.IntentReadWrite, claimproducer.IntentReadWrite, sem) {
				t.Fatalf("%s w×w must conflict", sem)
			}
		})
	}

	staged := claimproducer.WriteSemanticsStagedAsync
	t.Run("staged_async-r-r", func(t *testing.T) {
		if !ModeCoexists(claimproducer.IntentRead, claimproducer.IntentRead, staged) {
			t.Fatal("staged_async r×r must coexist")
		}
	})
	t.Run("staged_async-r-w", func(t *testing.T) {
		if !ModeCoexists(claimproducer.IntentRead, claimproducer.IntentReadWrite, staged) {
			t.Fatal("staged_async r×w must coexist")
		}
	})
	t.Run("staged_async-w-r", func(t *testing.T) {
		if !ModeCoexists(claimproducer.IntentReadWrite, claimproducer.IntentRead, staged) {
			t.Fatal("staged_async w×r must coexist")
		}
	})
	t.Run("staged_async-w-w", func(t *testing.T) {
		if ModeCoexists(claimproducer.IntentReadWrite, claimproducer.IntentReadWrite, staged) {
			t.Fatal("staged_async w×w must conflict")
		}
	})
}

func TestModeCoexistsPanicsOnUnknownWriteSemantics(t *testing.T) {
	cases := []struct {
		name string
		sem  claimproducer.WriteSemantics
	}{
		{"zero value", claimproducer.WriteSemanticsUnknown},
		{"unrecognized non-empty value", claimproducer.WriteSemantics("not-a-real-semantics")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("ModeCoexists(%q) did not panic; the zero/unknown write-semantics value must never reach the coexistence matrix", tc.sem)
				}
			}()
			ModeCoexists(claimproducer.IntentRead, claimproducer.IntentRead, tc.sem)
			t.Fatalf("unreachable: ModeCoexists(%q) should have panicked before returning", tc.sem)
		})
	}
}
