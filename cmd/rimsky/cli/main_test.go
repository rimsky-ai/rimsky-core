// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rimsky-cli-stdin")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "stdin")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		panic(err)
	}
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	os.Stdin = f
	code := m.Run()
	_ = f.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
