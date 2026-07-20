// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestClaimProducer_InterfaceCarriesNoRetryOrBudgetMethod(t *testing.T) {
	want := []string{
		"Abandon",
		"Capabilities",
		"Commit",
		"Name",
		"Open",
		"Release",
		"ScopesConflict",
		"SplitScope",
	}

	typ := reflect.TypeOf((*ClaimProducer)(nil)).Elem()
	got := make([]string, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		got[i] = typ.Method(i).Name
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimProducer method set = %v, want %v (retry/error-budget policy lives at the node level, "+
			"in lib/graph/node and lib/runtime, not on the store; a new method here is a signal that budget "+
			"logic is leaking into the ClaimProducer boundary)", got, want)
	}

	for _, name := range got {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "retry") || strings.Contains(lower, "budget") {
			t.Fatalf("ClaimProducer method %q looks retry/budget-shaped; retry-budget accounting belongs to "+
				"the node's error_types policy, not the store", name)
		}
	}
}
