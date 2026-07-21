// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestMigrate_ForeignKeyViolationAbortsAndRestoresPragma(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbIface, err := open(ctx, persistence.SQLiteConfig{Path: filepath.Join(dir, "fkguard.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	d, ok := dbIface.(*database)
	if !ok {
		t.Fatal("open did not return a *database")
	}
	t.Cleanup(func() { _ = d.Close() })
	d.db.SetMaxOpenConns(1)

	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	badSQL := `
INSERT INTO rimsky_templates (id, spec, state, source) VALUES ('fk-guard-tpl', '{}', 'registered', 'direct');
INSERT INTO rimsky_instances (id, template_hash) VALUES ('fk-guard-inst', 'fk-guard-tpl');
INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES ('fk-guard-node', 'fk-guard-inst', 'fixture');
DELETE FROM rimsky_instances WHERE id = 'fk-guard-inst';
`
	badMigrator := newMigrator(d.db)
	badMigrator.FS = fstest.MapFS{
		"999-fk-guard-fixture.sql": &fstest.MapFile{Data: []byte(badSQL)},
	}

	err = badMigrator.Run(ctx, d.c, shared.SilentLogger{})
	if err == nil {
		t.Fatal("expected the FK-orphaning migration to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "foreign_key_check") {
		t.Fatalf("error = %v, want it to name the foreign_key_check guard", err)
	}

	var orphanCount int
	if err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_nodes WHERE id = 'fk-guard-node'`).Scan(&orphanCount); err != nil {
		t.Fatalf("count orphan node: %v", err)
	}
	if orphanCount != 0 {
		t.Fatalf("a rejected migration must roll back entirely: found %d orphaned rimsky_nodes row(s)", orphanCount)
	}

	var fk int
	if err := d.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after a rejected migration, want 1 "+
			"(the deferred re-enable must run even on the failOnForeignKeyViolations error path)", fk)
	}
}
