// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestOpenWriteTransactionDoesNotStarveAConcurrentReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := open(ctx, persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "starvation.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	writeOpen := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			close(writeOpen)
			<-release
			return nil
		})
	}()
	<-writeOpen

	var one int
	if err := d.(*database).db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("a reader on a second pool connection could not run while a write transaction was open: %v", err)
	}
	if one != 1 {
		t.Fatalf("the concurrent reader returned %d, want 1", one)
	}

	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("the write transaction: %v", err)
	}
}
