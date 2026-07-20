// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type reservedFieldCase struct {
	number protoreflect.FieldNumber
	name   protoreflect.Name
}

func assertFieldsReserved(t *testing.T, msgName string, desc protoreflect.MessageDescriptor, cases []reservedFieldCase) {
	t.Helper()
	ranges := desc.ReservedRanges()
	names := desc.ReservedNames()
	for _, tc := range cases {
		if !ranges.Has(tc.number) {
			t.Errorf("%s: field number %d (was %q) is not reserved; a retired field number must never be reassigned",
				msgName, tc.number, tc.name)
		}
		if !names.Has(tc.name) {
			t.Errorf("%s: field name %q is not reserved; a retired field name must never be reassigned", msgName, tc.name)
		}
		if field := desc.Fields().ByNumber(tc.number); field != nil {
			t.Errorf("%s: field number %d is in active use by %q; it must stay reserved", msgName, tc.number, field.Name())
		}
		if field := desc.Fields().ByName(tc.name); field != nil {
			t.Errorf("%s: field name %q is in active use at number %d; it must stay reserved", msgName, tc.name, field.Number())
		}
	}
}
