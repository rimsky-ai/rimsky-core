// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTemplateNodeDef_ClaimProducersDirectiveIsClaimProducersNotStores(t *testing.T) {
	const withClaimProducers = "type: worker\n" +
		"claim_producers:\n" +
		"  - name: content\n" +
		"    selector: /region\n" +
		"    intent: rw\n"
	var withDirective TemplateNodeDef
	if err := yaml.Unmarshal([]byte(withClaimProducers), &withDirective); err != nil {
		t.Fatalf("yaml.Unmarshal(claim_producers): %v", err)
	}
	if len(withDirective.ClaimProducers) != 1 {
		t.Fatalf("claim_producers: directive did not bind to TemplateNodeDef.ClaimProducers: got %d entries, want 1",
			len(withDirective.ClaimProducers))
	}
	if withDirective.ClaimProducers[0].Name != "content" {
		t.Fatalf("ClaimProducers[0].Name = %q, want %q", withDirective.ClaimProducers[0].Name, "content")
	}

	const withLegacyStores = "type: worker\n" +
		"stores:\n" +
		"  - name: content\n" +
		"    selector: /region\n" +
		"    intent: rw\n"
	var withLegacy TemplateNodeDef
	if err := yaml.Unmarshal([]byte(withLegacyStores), &withLegacy); err != nil {
		t.Fatalf("yaml.Unmarshal(stores): %v", err)
	}
	if len(withLegacy.ClaimProducers) != 0 {
		t.Fatalf("the legacy 'stores:' spelling must NOT bind to TemplateNodeDef.ClaimProducers "+
			"(the current directive vocabulary is claim_producers: only): got %d entries, want 0",
			len(withLegacy.ClaimProducers))
	}
}
