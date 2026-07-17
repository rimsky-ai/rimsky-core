// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestDeploymentCA(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	tables := d.Tables()
	caTable := tables.DeploymentCA()

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	t.Run("GetBeforeCreateReturnsNotFound", func(t *testing.T) {
		_, ok, err := caTable.Get(ctx, nil)
		if err != nil {
			t.Fatalf("Get empty: %v", err)
		}
		if ok {
			t.Fatalf("Get must return ok=false before any CA exists")
		}
	})

	first := persistence.DeploymentCA{
		CACertPEM:      []byte("cert-pem-ONE"),
		CAKeyEncrypted: []byte("enc-key-ONE"),
		CreatedAt:      now,
	}
	second := persistence.DeploymentCA{
		CACertPEM:      []byte("cert-pem-TWO"),
		CAKeyEncrypted: []byte("enc-key-TWO"),
		CreatedAt:      now.Add(time.Hour),
	}

	t.Run("GetOrCreateIsIdempotent", func(t *testing.T) {
		got1, err := caTable.GetOrCreate(ctx, first, nil)
		if err != nil {
			t.Fatalf("GetOrCreate first: %v", err)
		}
		if !bytes.Equal(got1.CACertPEM, first.CACertPEM) {
			t.Fatalf("first create cert mismatch: got %q", got1.CACertPEM)
		}
		if got1.ID != persistence.DeploymentCASingletonID {
			t.Fatalf("singleton id mismatch: got %v", got1.ID)
		}

		got2, err := caTable.GetOrCreate(ctx, second, nil)
		if err != nil {
			t.Fatalf("GetOrCreate second: %v", err)
		}
		if !bytes.Equal(got2.CACertPEM, first.CACertPEM) {
			t.Fatalf("second GetOrCreate must return the first CA, got %q", got2.CACertPEM)
		}
		if !bytes.Equal(got2.CAKeyEncrypted, first.CAKeyEncrypted) {
			t.Fatalf("second GetOrCreate must return the first encrypted key, got %q", got2.CAKeyEncrypted)
		}
	})

	t.Run("GetReturnsPersistedRow", func(t *testing.T) {
		got, ok, err := caTable.Get(ctx, nil)
		if err != nil || !ok {
			t.Fatalf("Get after create: ok=%v err=%v", ok, err)
		}
		if !bytes.Equal(got.CACertPEM, first.CACertPEM) || !bytes.Equal(got.CAKeyEncrypted, first.CAKeyEncrypted) {
			t.Fatalf("persisted row mismatch: %+v", got)
		}
		if !got.CreatedAt.Equal(now) {
			t.Fatalf("created_at mismatch: got %v want %v", got.CreatedAt, now)
		}
	})
}
