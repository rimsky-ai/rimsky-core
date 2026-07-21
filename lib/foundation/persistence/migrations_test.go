// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
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
		QueryHas: func(_ context.Context, filename string) (bool, error) {
			return applied[filename], nil
		},
		ApplyOne: func(_ context.Context, _ string, filename string) error {
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
		QueryHas: func(context.Context, string) (bool, error) { return false, nil },
		ApplyOne: func(context.Context, string, string) error { return nil },
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
		FS:       fsys,
		QueryHas: func(context.Context, string) (bool, error) { return false, nil },
		ApplyOne: func(_ context.Context, _ string, filename string) error {
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

func TestMigratorRun_QueryHasErrorPropagates(t *testing.T) {
	wantErr := errors.New("query boom")
	m := Migrator{
		FS:       fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: []byte("-- first")}},
		QueryHas: func(context.Context, string) (bool, error) { return false, wantErr },
		ApplyOne: func(context.Context, string, string) error { return nil },
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run QueryHas error: got %v, want wrapping %v", err, wantErr)
	}
}

func TestMigratorRun_ReleaseFailureDoesNotFailRunAndIsSafeWithNilLogger(t *testing.T) {
	locker := &fakeMigratorLocker{releaseErr: errors.New("release boom")}
	m := Migrator{
		FS:       fstest.MapFS{"001-first.sql": &fstest.MapFile{Data: []byte("-- first")}},
		QueryHas: func(context.Context, string) (bool, error) { return false, nil },
		ApplyOne: func(context.Context, string, string) error { return nil },
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
		FS:       fstest.MapFS{"not-sql.txt": &fstest.MapFile{Data: []byte("not a migration")}},
		QueryHas: func(context.Context, string) (bool, error) { return false, nil },
		ApplyOne: func(context.Context, string, string) error { return nil },
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
		QueryHas: func(_ context.Context, filename string) (bool, error) {
			return applied[filename], nil
		},
		ApplyOne: func(_ context.Context, _ string, filename string) error {
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
		FS:       fstest.MapFS{},
		QueryHas: func(context.Context, string) (bool, error) { return false, nil },
		ApplyOne: func(context.Context, string, string) error { return nil },
	}
	err := m.Run(context.Background(), &fakeMigratorLocker{acquireErr: wantErr}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run acquire-lock error: got %v, want wrapping %v", err, wantErr)
	}
}
