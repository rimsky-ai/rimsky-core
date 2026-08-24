// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeMigratorLocker struct {
	acquireErr  error
	releaseErr  error
	releaseCall int
}

func (f *fakeMigratorLocker) TrySchedulerTick(context.Context) (bool, func(), error) {
	return false, func() {}, nil
}

func (f *fakeMigratorLocker) AcquireMigrationLock(context.Context) (func() error, error) {
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	return func() error {
		f.releaseCall++
		return f.releaseErr
	}, nil
}

func (f *fakeMigratorLocker) TakeNamedLock(context.Context, string, Tx) error {
	return nil
}

func (f *fakeMigratorLocker) TakeClaimScopeLock(context.Context, string, []byte, Tx) error {
	return nil
}

func (f *fakeMigratorLocker) TakeLifecycleScopeLock(context.Context, LifecycleScopeKind, string, Tx) error {
	return nil
}

var _ AdvisoryLocker = (*fakeMigratorLocker)(nil)

func TestMigratorRun_LexicographicOrderSkipsAppliedAndNonSQL(t *testing.T) {
	fsys := fstest.MapFS{
		"002-second.sql": &fstest.MapFile{Data: []byte("-- second")},
		"001-first.sql":  &fstest.MapFile{Data: []byte("-- first")},
		"not-sql.txt":    &fstest.MapFile{Data: []byte("ignore me")},
	}
	applied := map[string]bool{"001-first.sql": true}
	var appliedOrder []string
	m := Migrator{
		FS: fsys,
		QueryApplied: func(_ context.Context, filename string) (bool, string, error) {
			if !applied[filename] {
				return false, "", nil
			}
			data, err := fs.ReadFile(fsys, filename)
			if err != nil {
				return false, "", err
			}
			return true, MigrationDigest(data), nil
		},
		ApplyOne: func(_ context.Context, _ string, filename string, _ string) error {
			appliedOrder = append(appliedOrder, filename)
			return nil
		},
	}
	if err := m.Run(context.Background(), &fakeMigratorLocker{}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(appliedOrder) != 1 || appliedOrder[0] != "002-second.sql" {
		t.Fatalf("appliedOrder = %v, want [002-second.sql] (already-applied 001 skipped, non-.sql ignored)", appliedOrder)
	}
}

func TestMigratorRun_BootstrapErrorPropagates(t *testing.T) {
	wantErr := errors.New("bootstrap boom")
	m := Migrator{
		FS: fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: []byte("-- first")}},
		Bootstrap: func(context.Context) error {
			return wantErr
		},
		QueryApplied: func(context.Context, string) (bool, string, error) { return false, "", nil },
		ApplyOne:     func(context.Context, string, string, string) error { return nil },
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run bootstrap error: got %v, want wrapping %v", err, wantErr)
	}
}

func TestMigratorRun_ApplyOneErrorStopsAtFirstFailure(t *testing.T) {
	wantErr := errors.New("apply boom")
	fsys := fstest.MapFS{
		"001-first.sql":  &fstest.MapFile{Data: []byte("-- first")},
		"002-second.sql": &fstest.MapFile{Data: []byte("-- second")},
	}
	var applyCalls []string
	m := Migrator{
		FS:           fsys,
		QueryApplied: func(context.Context, string) (bool, string, error) { return false, "", nil },
		ApplyOne: func(_ context.Context, _ string, filename string, _ string) error {
			applyCalls = append(applyCalls, filename)
			if filename == "001-first.sql" {
				return wantErr
			}
			return nil
		},
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run apply error: got %v, want wrapping %v", err, wantErr)
	}
	if len(applyCalls) != 1 {
		t.Fatalf("ApplyOne calls = %v, want exactly [001-first.sql] (must stop at first failure)", applyCalls)
	}
}

func TestMigratorRun_QueryAppliedErrorPropagates(t *testing.T) {
	wantErr := errors.New("query boom")
	m := Migrator{
		FS:           fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: []byte("-- first")}},
		QueryApplied: func(context.Context, string) (bool, string, error) { return false, "", wantErr },
		ApplyOne:     func(context.Context, string, string, string) error { return nil },
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run QueryApplied error: got %v, want wrapping %v", err, wantErr)
	}
}

func TestMigratorRun_ReleaseFailureDoesNotFailRunAndIsSafeWithNilLogger(t *testing.T) {
	locker := &fakeMigratorLocker{releaseErr: errors.New("release boom")}
	m := Migrator{
		FS:           fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: []byte("-- first")}},
		QueryApplied: func(context.Context, string) (bool, string, error) { return false, "", nil },
		ApplyOne:     func(context.Context, string, string, string) error { return nil },
	}
	if err := m.Run(context.Background(), locker, nil); err != nil {
		t.Fatalf("Run: %v, want nil (release failure must not fail Run)", err)
	}
	if locker.releaseCall != 1 {
		t.Fatalf("release called %d times, want 1", locker.releaseCall)
	}
}

func TestMigratorRun_EmptyFSIsAStartupError(t *testing.T) {
	m := Migrator{
		FS:           fstest.MapFS{"not-sql.txt": &fstest.MapFile{Data: []byte("not a migration")}},
		QueryApplied: func(context.Context, string) (bool, string, error) { return false, "", nil },
		ApplyOne:     func(context.Context, string, string, string) error { return nil },
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if err == nil {
		t.Fatalf("Run: got nil error, want a startup error for zero .sql migration files")
	}
}

func TestMigratorRun_OutOfOrderFileIsRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"021-later.sql": &fstest.MapFile{Data: []byte("-- later")},
		"005-early.sql": &fstest.MapFile{Data: []byte("-- early")},
	}
	applied := map[string]bool{"021-later.sql": true}
	var applyCalls []string
	m := Migrator{
		FS: fsys,
		QueryApplied: func(_ context.Context, filename string) (bool, string, error) {
			if !applied[filename] {
				return false, "", nil
			}
			data, err := fs.ReadFile(fsys, filename)
			if err != nil {
				return false, "", err
			}
			return true, MigrationDigest(data), nil
		},
		ApplyOne: func(_ context.Context, _ string, filename string, _ string) error {
			applyCalls = append(applyCalls, filename)
			return nil
		},
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if err == nil {
		t.Fatalf("Run: got nil error, want a rejection of 005-early.sql sorting below applied 021-later.sql")
	}
	if len(applyCalls) != 0 {
		t.Fatalf("ApplyOne calls = %v, want none (out-of-order file must be rejected before applying)", applyCalls)
	}
}

func TestMigratorRun_AcquireLockErrorPropagates(t *testing.T) {
	wantErr := errors.New("lock boom")
	m := Migrator{
		FS:           fstest.MapFS{},
		QueryApplied: func(context.Context, string) (bool, string, error) { return false, "", nil },
		ApplyOne:     func(context.Context, string, string, string) error { return nil },
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{acquireErr: wantErr}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run acquire-lock error: got %v, want wrapping %v", err, wantErr)
	}
}

// @decision: migrations-append-only-numbered
func TestMigratorRun_RejectsAMigrationRewrittenAfterItWasApplied(t *testing.T) {
	fsys := fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: []byte("-- rewritten")}}
	var applyCalls []string
	m := Migrator{
		FS: fsys,
		QueryApplied: func(context.Context, string) (bool, string, error) {
			return true, MigrationDigest([]byte("-- as applied")), nil
		},
		ApplyOne: func(_ context.Context, _ string, filename string, _ string) error {
			applyCalls = append(applyCalls, filename)
			return nil
		},
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if err == nil {
		t.Fatalf("Run: got nil error, want a refusal naming the rewritten file")
	}
	if !strings.Contains(err.Error(), "001-first.sql") {
		t.Fatalf("Run error must name the changed file; got %v", err)
	}
	if len(applyCalls) != 0 {
		t.Fatalf("ApplyOne calls = %v, want none", applyCalls)
	}
}

// @decision: migrations-append-only-numbered
func TestMigratorRun_BackfillsADigestForAnAppliedRowThatHasNone(t *testing.T) {
	contents := []byte("-- first")
	fsys := fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: contents}}
	backfilled := map[string]string{}
	m := Migrator{
		FS: fsys,
		QueryApplied: func(context.Context, string) (bool, string, error) {
			return true, "", nil
		},
		RecordDigest: func(_ context.Context, filename string, digest string) error {
			backfilled[filename] = digest
			return nil
		},
		ApplyOne: func(context.Context, string, string, string) error {
			t.Fatalf("an applied file must not be re-applied during a digest backfill")
			return nil
		},
	}
	if err := m.Run(context.Background(), &fakeMigratorLocker{}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := backfilled["001-first.sql"]; got != MigrationDigest(contents) {
		t.Fatalf("backfilled digest = %q, want %q", got, MigrationDigest(contents))
	}
}
