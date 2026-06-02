// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAggregationPolicyYAMLBinds guards the CLI template path: a template
// author writes the documented snake_case keys (cancel_siblings,
// max_failures) and the CLI's gopkg.in/yaml.v3 Unmarshal
// (cmd/rimsky/cli/templates.go::readSpecFile) must bind them onto the
// struct fields. Without explicit yaml: tags, yaml.v3 falls back to the
// lowercased field names (cancelsiblings/maxfailures), silently dropping
// the documented keys and leaving both fields at their zero value.
func TestAggregationPolicyYAMLBinds(t *testing.T) {
	const fragment = "kind: strict\ncancel_siblings: true\nmax_failures: 3\n"
	var p AggregationPolicy
	if err := yaml.Unmarshal([]byte(fragment), &p); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if p.Kind != AggregationKindStrict {
		t.Errorf("Kind = %q, want %q", p.Kind, AggregationKindStrict)
	}
	if !p.CancelSiblings {
		t.Errorf("CancelSiblings = false, want true (cancel_siblings key did not bind)")
	}
	if p.MaxFailures != 3 {
		t.Errorf("MaxFailures = %d, want 3 (max_failures key did not bind)", p.MaxFailures)
	}
}

func TestAggregationPolicy_Validate(t *testing.T) {
	cases := []struct {
		name    string
		policy  AggregationPolicy
		wantErr bool
	}{
		{"strict with cancel siblings", AggregationPolicy{Kind: AggregationKindStrict, CancelSiblings: true}, false},
		{"strict bare", AggregationPolicy{Kind: AggregationKindStrict}, false},
		{"threshold with max_failures", AggregationPolicy{Kind: AggregationKindThreshold, MaxFailures: 3}, false},
		{"threshold missing max_failures", AggregationPolicy{Kind: AggregationKindThreshold}, true},
		{"threshold with cancel_siblings", AggregationPolicy{Kind: AggregationKindThreshold, MaxFailures: 1, CancelSiblings: true}, true},
		{"best_effort", AggregationPolicy{Kind: AggregationKindBestEffort}, false},
		{"first", AggregationPolicy{Kind: AggregationKindFirst}, false},
		{"empty kind", AggregationPolicy{}, true},
		{"unknown kind", AggregationPolicy{Kind: "unknown"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("got err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestParkReason_IsValid(t *testing.T) {
	valid := []ParkReason{
		ParkReasonAwaitCallback, ParkReasonSnooze,
	}
	for _, r := range valid {
		if !r.IsValid() {
			t.Errorf("ParkReason(%q).IsValid() = false, want true", r)
		}
	}
	if ParkReasonUnspecified.IsValid() {
		t.Errorf("ParkReasonUnspecified.IsValid() = true, want false")
	}
	if ParkReason("garbage").IsValid() {
		t.Errorf("garbage.IsValid() = true, want false")
	}
}
