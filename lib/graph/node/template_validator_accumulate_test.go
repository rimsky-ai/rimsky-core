// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"testing"
)

func TestValidateTemplate_AccumulatesAllErrors_NoBailOnFirst(t *testing.T) {
	spec := &TemplateSpec{
		Name:             "",
		Version:          "",
		MessageQueueMode: "bogus",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	if res.Ok() {
		t.Fatalf("expected validation errors, got none")
	}

	wantPrefixes := []string{"name", "version", "message_queue_mode"}
	for _, prefix := range wantPrefixes {
		hasErrorAt(t, res, prefix)
	}

	if got := len(res.Errors); got < len(wantPrefixes) {
		t.Fatalf("ValidateTemplate returned %d error(s) for %d independent violations "+
			"(%+v); registration must accumulate every violation, not bail after the first",
			got, len(wantPrefixes), res.Errors)
	}
}
