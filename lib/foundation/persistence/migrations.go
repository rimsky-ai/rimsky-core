// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: migrations-append-only-numbered
type Migrator struct {
	FS           fs.FS
	QueryApplied func(ctx context.Context, filename string) (applied bool, digest string, err error)
	Bootstrap    func(ctx context.Context) error
	ApplyOne     func(ctx context.Context, sql string, filename string, digest string) error
	RecordDigest func(ctx context.Context, filename string, digest string) error
}

func MigrationDigest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func (m Migrator) Run(ctx context.Context, advLock AdvisoryLocker, log shared.Logger) error {
	release, err := advLock.AcquireMigrationLock(ctx)
	if err != nil {
		return fmt.Errorf("persistence.Migrator: acquire lock: %w", err)
	}
	defer func() {
		if err := release(); err != nil && log != nil {
			log.Warn("PERSISTENCE.MIGRATIONLOCK.RELEASEFAILED", "site", "persistence.Migrator", "err", err)
		}
	}()

	if m.Bootstrap != nil {
		if err := m.Bootstrap(ctx); err != nil {
			return fmt.Errorf("persistence.Migrator: bootstrap: %w", err)
		}
	}

	entries, err := fs.ReadDir(m.FS, ".")
	if err != nil {
		return fmt.Errorf("persistence.Migrator: read embed fs: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("persistence.Migrator: no .sql migration files found in embedded FS root; refusing to boot against an unmigrated schema")
	}
	sort.Strings(files)

	contentsByFile := make(map[string][]byte, len(files))
	digestByFile := make(map[string]string, len(files))
	for _, filename := range files {
		sqlBytes, err := fs.ReadFile(m.FS, filename)
		if err != nil {
			return fmt.Errorf("persistence.Migrator: read %s: %w", filename, err)
		}
		contentsByFile[filename] = sqlBytes
		digestByFile[filename] = MigrationDigest(sqlBytes)
	}

	hasByFile := make(map[string]bool, len(files))
	var maxApplied string
	for _, filename := range files {
		has, recorded, err := m.QueryApplied(ctx, filename)
		if err != nil {
			return fmt.Errorf("persistence.Migrator: check %s: %w", filename, err)
		}
		hasByFile[filename] = has
		if !has {
			continue
		}
		if filename > maxApplied {
			maxApplied = filename
		}
		// @decision: migrations-append-only-numbered
		if recorded == "" {
			if m.RecordDigest == nil {
				continue
			}
			if err := m.RecordDigest(ctx, filename, digestByFile[filename]); err != nil {
				return fmt.Errorf("persistence.Migrator: backfill digest for %s: %w", filename, err)
			}
			continue
		}
		if recorded != digestByFile[filename] {
			return fmt.Errorf("persistence.Migrator: %s changed after it was applied (recorded digest %s, current %s); an applied migration is immutable", filename, recorded, digestByFile[filename])
		}
	}

	applied := 0
	for _, filename := range files {
		if hasByFile[filename] {
			continue
		}
		// @decision: migrations-append-only-numbered
		if maxApplied != "" && filename < maxApplied {
			return fmt.Errorf("persistence.Migrator: %s sorts before already-applied %s; migrations are append-only and must sort after every applied file", filename, maxApplied)
		}
		if err := m.ApplyOne(ctx, string(contentsByFile[filename]), filename, digestByFile[filename]); err != nil {
			return fmt.Errorf("persistence.Migrator: apply %s: %w", filename, err)
		}
		if log != nil {
			log.Info("PERSISTENCE.MIGRATION.APPLIED", "filename", filename)
		}
		applied++
	}
	if log != nil {
		if applied == 0 {
			log.Info("PERSISTENCE.MIGRATIONS.UPTODATE")
		} else {
			log.Info("PERSISTENCE.MIGRATIONS.COMPLETED", "applied", applied)
		}
	}
	return nil
}
