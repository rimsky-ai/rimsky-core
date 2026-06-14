// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// max_open_conns_test.go — regression coverage for the SQLite driver's
// connection-pool sizing (@decision: persistence-driver). The starvation
// case the wider pool exists to prevent is: one long-running write tx
// (the supervisor's settle path) holds its connection while a parallel
// read-only path (the control-api's wait-loop polls — answering GET
// /instances/{id} for the compose-run verb) wants a connection too. At
// MaxOpenConns=1 the read path waits until the writer commits; the test
// here proves the read makes progress concurrently with a held writer.

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

	// Set up a tiny table so the writer has something concrete to hold
	// a row-level lock on. With WAL + busy_timeout, the writer slot is
	// held at the database file level; the conn it sits on is what the
	// pool slots are sized against.
	if _, err := db.Exec(`CREATE TABLE poolprobe (id INTEGER PRIMARY KEY, payload TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Start a writer tx and hold it open. The release channel lets us
	// keep the connection occupied while we attempt the parallel read.
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

	// Now attempt a parallel read against the same *sql.DB. With
	// MaxOpenConns=1, db.QueryRowContext would block on the conn-pool
	// acquisition until the writer commits — and the 1-second context
	// here would fire first. With MaxOpenConns=8 the read gets its own
	// conn and completes immediately.
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

	// Let the writer commit and wait for the goroutine to finish so
	// the test does not leave the writer hanging.
	close(release)
	wg.Wait()
	if writerErr != nil {
		t.Fatalf("writer commit: %v", writerErr)
	}
}
