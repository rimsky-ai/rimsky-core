// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import (
	"testing"
)

func TestPark_ReservesRetiredFieldNumbersAndNames(t *testing.T) {
	desc := (&Park{}).ProtoReflect().Descriptor()
	assertFieldsReserved(t, "Park", desc, []reservedFieldCase{
		{1, "reason"},
		{2, "payload"},
		{4, "session_token"},
		{5, "reason_note"},
		{6, "reason_label"},
	})
}
