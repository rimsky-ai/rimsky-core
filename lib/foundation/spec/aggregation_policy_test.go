// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAggregationPolicyYAMLBinds(t *testing.T) {
	const fragment = "kind: strict\nmax_failures: 3\n"
	var p AggregationPolicy
	if err := yaml.Unmarshal([]byte(fragment), &p); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if p.Kind != AggregationKindStrict {
		t.Errorf("Kind = %q, want %q", p.Kind, AggregationKindStrict)
	}
	if p.MaxFailures != 3 {
		t.Errorf("MaxFailures = %d, want 3 (max_failures key did not bind)", p.MaxFailures)
	}
}
