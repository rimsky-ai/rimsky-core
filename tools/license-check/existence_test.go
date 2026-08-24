// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyEntriesExist_FlagsMissingClassificationEntries(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"protocols/action", "runtime", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	cfg := writeLicensingYAML(t, dir, `apache:
  - protocols/
  - cmd/gone-apache/
agpl:
  - runtime/
  - cmd/gone-agpl/
exempt:
  - bin/
`)

	vs := verifyEntriesExist(cfg, dir)

	if len(vs) != 2 {
		t.Fatalf("want 2 violations (the two missing classification entries), got %d: %+v", len(vs), vs)
	}
	joined := vs[0].message + " " + vs[1].message
	for _, want := range []string{"cmd/gone-apache", "cmd/gone-agpl"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a violation naming %q; got %q", want, joined)
		}
	}
	for _, unwanted := range []string{"protocols", "runtime", "bin"} {
		if strings.Contains(joined, `"`+unwanted) {
			t.Errorf("did not expect a violation naming %q; got %q", unwanted, joined)
		}
	}
}

func TestVerifyEntriesExist_FlagsMissingExemptEntry(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"protocols", "runtime", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	cfg := writeLicensingYAML(t, dir, `apache:
  - protocols/
agpl:
  - runtime/
exempt:
  - bin/
  - LICENSE.missing
`)

	vs := verifyEntriesExist(cfg, dir)

	if len(vs) != 1 {
		t.Fatalf("want 1 violation (the missing exempt entry), got %d: %+v", len(vs), vs)
	}
	if !strings.Contains(vs[0].message, "LICENSE.missing") {
		t.Errorf("expected the violation to name %q; got %q", "LICENSE.missing", vs[0].message)
	}
	if !strings.Contains(vs[0].message, "exempt") {
		t.Errorf("expected the violation to identify the exempt bucket; got %q", vs[0].message)
	}
	if strings.Contains(vs[0].message, `"bin"`) {
		t.Errorf("did not expect a violation naming present entry \"bin\"; got %q", vs[0].message)
	}
}

func TestVerifyEntriesExist_AllPresent(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"protocols", "runtime/service", "runtime", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	cfg := writeLicensingYAML(t, dir, `apache:
  - protocols/
  - runtime/service/
agpl:
  - runtime/
exempt:
  - bin/
`)
	if vs := verifyEntriesExist(cfg, dir); len(vs) != 0 {
		t.Fatalf("want 0 violations, got %d: %+v", len(vs), vs)
	}
}
