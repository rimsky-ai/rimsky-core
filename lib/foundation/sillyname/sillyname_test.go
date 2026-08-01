// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sillyname

import "testing"

func TestGenerateShapeMatchesValidator(t *testing.T) {
	for i := 0; i < 100; i++ {
		got := Generate()
		if err := Validate(got); err != nil {
			t.Fatalf("Generate() produced %q which fails Validate: %v", got, err)
		}
	}
}

func TestValidateAcceptsWellFormedLabels(t *testing.T) {
	cases := []string{"sparkling-wombat", "quiet-otter", "brave-crocodile", "sad-panda", "adjective-noun-suffix", "one-two-three-four"}
	for _, c := range cases {
		if err := Validate(c); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", c, err)
		}
	}
}

func TestValidateRejectsMalformedLabels(t *testing.T) {
	cases := []string{"", "single", "UPPER-CASE", "trailing-", "-leading", "with space", "digits-1", "a-b-c-d-e", "sparkling--wombat"}
	for _, c := range cases {
		if err := Validate(c); err == nil {
			t.Errorf("Validate(%q) = nil, want error", c)
		}
	}
}
