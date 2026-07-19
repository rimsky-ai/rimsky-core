// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestPark_ReservesRetiredFieldNumbersAndNames(t *testing.T) {
	desc := (&Park{}).ProtoReflect().Descriptor()
	ranges := desc.ReservedRanges()
	names := desc.ReservedNames()

	cases := []struct {
		number protoreflect.FieldNumber
		name   protoreflect.Name
	}{
		{1, "reason"},
		{2, "payload"},
		{4, "session_token"},
		{5, "reason_note"},
		{6, "reason_label"},
	}
	for _, tc := range cases {
		if !ranges.Has(tc.number) {
			t.Errorf("Park: field number %d (was %q) is not reserved; a retired field number must never be reassigned", tc.number, tc.name)
		}
		if !names.Has(tc.name) {
			t.Errorf("Park: field name %q is not reserved; a retired field name must never be reassigned", tc.name)
		}
		if field := desc.Fields().ByNumber(tc.number); field != nil {
			t.Errorf("Park: field number %d is in active use by %q; it must stay reserved", tc.number, field.Name())
		}
		if field := desc.Fields().ByName(tc.name); field != nil {
			t.Errorf("Park: field name %q is in active use at number %d; it must stay reserved", tc.name, field.Number())
		}
	}
}
