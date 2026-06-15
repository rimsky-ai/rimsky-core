// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgsqlite "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

// TestSQLitePoolSizeIsWide_HeldWriterDoesNotStarveReader covers
// @decision: persistence-driver: a transaction holding its
// connection MUST NOT block a parallel read on a different connection
// from making progress. The previous MaxOpenConns=1 setting caused the
// read to queue behind the writer; the wider pool lets it acquire its
// own connection and complete. The falsifier this rules out is exactly
// the symptom the compose-run verb's terminal-wait loop hit before the
// fix — control-api request handlers receiving context-deadline-
// exceeded errors after ~30s under any sustained supervisor write
// activity.
func TestSQLitePoolSizeIsWide_HeldWriterDoesNotStarveReader(t *testing.T) {
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "pool.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	db := pgsqlite.DBFromDatabase(d)

	// @deliberate: probe table gives the writer something concrete to hold; under
	// WAL + busy_timeout the writer slot is held at the file level, so the conn
	// it sits on is what the pool-slot sizing is measured against.
	if _, err := db.Exec(`CREATE TABLE poolprobe (id INTEGER PRIMARY KEY, payload TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	release := make(chan struct{})
	writerStarted := make(chan struct{})
	var writerErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			writerErr = txErr
			close(writerStarted)
			return
		}
		if _, txErr = tx.Exec(`INSERT INTO poolprobe(payload) VALUES ('writer-row')`); txErr != nil {
			_ = tx.Rollback()
			writerErr = txErr
			close(writerStarted)
			return
		}
		close(writerStarted)
		<-release
		writerErr = tx.Commit()
	}()
	<-writerStarted
	if writerErr != nil {
		t.Fatalf("writer tx setup: %v", writerErr)
	}

	// @constraint: with MaxOpenConns=1 this QueryRowContext would block on
	// conn-pool acquisition until the writer commits and the 1s context would
	// fire first; the wider pool lets the read get its own conn and complete.
	readCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(readCtx, `SELECT 1`).Scan(&one); err != nil {
		close(release)
		wg.Wait()
		t.Fatalf("parallel read starved by held writer: %v", err)
	}
	if one != 1 {
		t.Errorf("read returned %d, want 1", one)
	}

	close(release)
	wg.Wait()
	if writerErr != nil {
		t.Fatalf("writer commit: %v", writerErr)
	}
}
