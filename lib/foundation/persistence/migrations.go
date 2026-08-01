// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type Migrator struct {
	FS        fs.FS
	QueryHas  func(ctx context.Context, filename string) (bool, error)
	Bootstrap func(ctx context.Context) error
	ApplyOne  func(ctx context.Context, sql string, filename string) error
}

func (m Migrator) Run(ctx context.Context, advLock AdvisoryLocker, log shared.Logger) error {
	release, err := advLock.AcquireMigrationLock(ctx)
	if err != nil {
		return fmt.Errorf("persistence.Migrator: acquire lock: %w", err)
	}
	defer func() {
		if err := release(); err != nil && log != nil {
			log.Warn("persistence.Migrator: release migration lock", "err", err)
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

	hasByFile := make(map[string]bool, len(files))
	var maxApplied string
	for _, filename := range files {
		has, err := m.QueryHas(ctx, filename)
		if err != nil {
			return fmt.Errorf("persistence.Migrator: check %s: %w", filename, err)
		}
		hasByFile[filename] = has
		if has && filename > maxApplied {
			maxApplied = filename
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
		sqlBytes, err := fs.ReadFile(m.FS, filename)
		if err != nil {
			return fmt.Errorf("persistence.Migrator: read %s: %w", filename, err)
		}
		if err := m.ApplyOne(ctx, string(sqlBytes), filename); err != nil {
			return fmt.Errorf("persistence.Migrator: apply %s: %w", filename, err)
		}
		if log != nil {
			log.Info("migration applied", "filename", filename)
		}
		applied++
	}
	if log != nil {
		if applied == 0 {
			log.Info("no migrations to apply")
		} else {
			log.Info("migrations complete", "applied", applied)
		}
	}
	return nil
}
