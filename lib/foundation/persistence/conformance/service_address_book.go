// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service-address-book
package conformance

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testServiceAddressBookPublishGetList(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	firstGeneration := []persistence.ServiceAddressRow{
		{Kind: persistence.ServiceKindExecutor, Name: "executor-alpha", Transport: "grpc", Endpoint: "executor-alpha:9000", TLS: "required"},
		{Kind: persistence.ServiceKindExecutor, Name: "executor-beta", Transport: "http", Endpoint: "https://executor-beta:8443"},
		{Kind: persistence.ServiceKindClaimProducer, Name: "store-alpha", Transport: "grpc", Endpoint: "store-alpha:9100"},
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ServiceAddressBook().PublishAll(ctx, firstGeneration, tx)
	}); err != nil {
		t.Fatalf("PublishAll(first generation): %v", err)
	}

	var got *persistence.ServiceAddressRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ServiceAddressBook().Get(ctx, persistence.ServiceKindExecutor, "executor-alpha", tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get(executor-alpha): %v", err)
	}
	if got == nil {
		t.Fatalf("Get(executor-alpha) after PublishAll: got nil row")
	}
	if got.Transport != "grpc" || got.Endpoint != "executor-alpha:9000" || got.TLS != "required" {
		t.Fatalf("Get(executor-alpha) = %+v, want transport/endpoint/tls round-tripped", got)
	}

	var sameNameOtherKind *persistence.ServiceAddressRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ServiceAddressBook().Get(ctx, persistence.ServiceKindClaimProducer, "executor-alpha", tx)
		sameNameOtherKind = r
		return err
	}); err != nil {
		t.Fatalf("Get(claim_producer, executor-alpha): %v", err)
	}
	if sameNameOtherKind != nil {
		t.Fatalf("Get keyed only by name, not (kind, name): %+v", sameNameOtherKind)
	}

	secondGeneration := []persistence.ServiceAddressRow{
		{Kind: persistence.ServiceKindExecutor, Name: "executor-alpha", Transport: "grpc", Endpoint: "executor-alpha:9999"},
		{Kind: persistence.ServiceKindClaimProducer, Name: "store-beta", Transport: "grpc", Endpoint: "store-beta:9101"},
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ServiceAddressBook().PublishAll(ctx, secondGeneration, tx)
	}); err != nil {
		t.Fatalf("PublishAll(second generation): %v", err)
	}

	var listed []persistence.ServiceAddressRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.ServiceAddressBook().List(ctx, tx)
		listed = rows
		return err
	}); err != nil {
		t.Fatalf("List after re-publish: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List after re-publish = %+v, want exactly the second generation (PublishAll replaces the full catalog)", listed)
	}
	byKey := map[string]persistence.ServiceAddressRow{}
	for _, r := range listed {
		byKey[r.Kind+"/"+r.Name] = r
	}
	if r, ok := byKey["executor/executor-alpha"]; !ok || r.Endpoint != "executor-alpha:9999" {
		t.Fatalf("re-published executor-alpha = %+v, want the second generation's endpoint", byKey)
	}
	if _, ok := byKey["claim_producer/store-alpha"]; ok {
		t.Fatalf("store-alpha survived a re-publish that omitted it — PublishAll must replace the full catalog: %+v", listed)
	}
	if _, ok := byKey["claim_producer/store-beta"]; !ok {
		t.Fatalf("store-beta missing after re-publish: %+v", listed)
	}

	if err := store.ServiceAddressBook().PublishAll(ctx, secondGeneration, nil); err == nil {
		t.Fatalf("PublishAll(nil tx) must be rejected: the DELETE-then-INSERT rewrite is only atomic inside a transaction")
	}
}
