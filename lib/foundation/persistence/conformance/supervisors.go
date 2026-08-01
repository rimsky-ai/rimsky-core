// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testSupervisorsRegisterGetListUnregisterRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	idA := "conformance-supervisor-a-" + uuid.NewString()
	idB := "conformance-supervisor-b-" + uuid.NewString()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{
			ID:           idA,
			Concurrency:  4,
			CallbackHost: "supervisor-a.internal",
			CallbackPort: 9001,
		}, tx)
	}); err != nil {
		t.Fatalf("Register(A): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{
			ID:          idB,
			Concurrency: 2,
		}, tx)
	}); err != nil {
		t.Fatalf("Register(B): %v", err)
	}

	var rowA *persistence.SupervisorRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Supervisors().Get(ctx, idA, tx)
		rowA = r
		return err
	}); err != nil {
		t.Fatalf("Get(A): %v", err)
	}
	if rowA == nil {
		t.Fatalf("Get(A) after Register: got nil row")
	}
	if rowA.Concurrency != 4 {
		t.Fatalf("Get(A).Concurrency = %d, want 4", rowA.Concurrency)
	}
	if rowA.CallbackHost != "supervisor-a.internal" || rowA.CallbackPort != 9001 {
		t.Fatalf("Get(A) callback = %s:%d, want supervisor-a.internal:9001", rowA.CallbackHost, rowA.CallbackPort)
	}
	if rowA.RegisteredAt.IsZero() {
		t.Fatalf("Get(A).RegisteredAt is zero, want a real timestamp")
	}

	var listed []persistence.SupervisorRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.Supervisors().List(ctx, tx)
		listed = rows
		return err
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	indexOf := map[string]int{}
	for i, r := range listed {
		indexOf[r.ID] = i
	}
	idxA, okA := indexOf[idA]
	idxB, okB := indexOf[idB]
	if !okA {
		t.Fatalf("List after Register(A,B) is missing %s: %+v", idA, listed)
	}
	if !okB {
		t.Fatalf("List after Register(A,B) is missing %s: %+v", idB, listed)
	}
	if idxA > idxB {
		t.Fatalf("List order violates registered_at ASC: %s (registered first) came after %s: %+v", idA, idB, listed)
	}

	originalRegisteredAt := rowA.RegisteredAt
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{
			ID:           idA,
			Concurrency:  8,
			CallbackHost: "supervisor-a-v2.internal",
			CallbackPort: 9002,
		}, tx)
	}); err != nil {
		t.Fatalf("Register(A) re-register: %v", err)
	}

	var rowA2 *persistence.SupervisorRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Supervisors().Get(ctx, idA, tx)
		rowA2 = r
		return err
	}); err != nil {
		t.Fatalf("Get(A) after re-register: %v", err)
	}
	if rowA2 == nil {
		t.Fatalf("Get(A) after re-register: got nil row")
	}
	if rowA2.Concurrency != 8 || rowA2.CallbackHost != "supervisor-a-v2.internal" || rowA2.CallbackPort != 9002 {
		t.Fatalf("Get(A) after re-register = %+v, want updated concurrency/callback", rowA2)
	}
	if !rowA2.RegisteredAt.Equal(originalRegisteredAt) {
		t.Fatalf("Get(A).RegisteredAt changed on re-register: got %v, want unchanged %v", rowA2.RegisteredAt, originalRegisteredAt)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Supervisors().Unregister(ctx, idA, tx)
	}); err != nil {
		t.Fatalf("Unregister(A): %v", err)
	}

	var rowAAfterUnregister *persistence.SupervisorRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Supervisors().Get(ctx, idA, tx)
		rowAAfterUnregister = r
		return err
	}); err != nil {
		t.Fatalf("Get(A) after Unregister: %v", err)
	}
	if rowAAfterUnregister != nil {
		t.Fatalf("Get(A) after Unregister = %+v, want nil", rowAAfterUnregister)
	}

	var listedAfterUnregister []persistence.SupervisorRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.Supervisors().List(ctx, tx)
		listedAfterUnregister = rows
		return err
	}); err != nil {
		t.Fatalf("List after Unregister(A): %v", err)
	}
	for _, r := range listedAfterUnregister {
		if r.ID == idA {
			t.Fatalf("List after Unregister(A) still contains A: %+v", listedAfterUnregister)
		}
	}
	sawB := false
	for _, r := range listedAfterUnregister {
		if r.ID == idB {
			sawB = true
		}
	}
	if !sawB {
		t.Fatalf("List after Unregister(A) is missing B: %+v", listedAfterUnregister)
	}
}
