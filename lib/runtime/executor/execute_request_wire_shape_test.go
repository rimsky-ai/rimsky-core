// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestExecuteRequest_NoNewFieldsBesidesTheDeclaredAllowlist(t *testing.T) {
	wantFieldNumbers := map[int32]string{
		1:  "node_id",
		2:  "instance_id",
		3:  "node_type",
		5:  "attributes",
		6:  "attributes_schema",
		7:  "claim_producers",
		8:  "callback_url",
		9:  "cancel_token",
		12: "dispatch_id",
		14: "prior_dispatch_id",
		15: "prior_dispatch_disposition",
		16: "run_scope_id",
		17: "scratch",
	}

	fields := (&genv1.ExecuteRequest{}).ProtoReflect().Descriptor().Fields()
	if fields.Len() != len(wantFieldNumbers) {
		t.Fatalf("ExecuteRequest has %d fields, want %d — per-node service config (e.g. cli.*) "+
			"must ride the existing `attributes` field (5), never a new dedicated proto field; "+
			"if this is an intentional wire-shape change, update this allowlist deliberately",
			fields.Len(), len(wantFieldNumbers))
	}
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		num := int32(f.Number())
		wantName, ok := wantFieldNumbers[num]
		if !ok {
			t.Errorf("unexpected new ExecuteRequest field %d (%q) not in the allowlist", num, f.Name())
			continue
		}
		if string(f.Name()) != wantName {
			t.Errorf("ExecuteRequest field %d renamed: got %q, want %q", num, f.Name(), wantName)
		}
	}
}
