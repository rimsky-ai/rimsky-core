// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyEntriesExist_FlagsMissingClassificationEntries(t *testing.T) {
	dir := t.TempDir()
	// Present entries (one apache, one agpl).
	for _, d := range []string{"protocols/action", "runtime"} {
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
	// Present classification entries must NOT be flagged, and the absent
	// exempt entry (bin/) must NOT be flagged — exempt is skip-rules.
	for _, unwanted := range []string{"protocols", "runtime", "bin"} {
		if strings.Contains(joined, `"`+unwanted) {
			t.Errorf("did not expect a violation naming %q; got %q", unwanted, joined)
		}
	}
}

func TestVerifyEntriesExist_AllPresent(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"protocols", "runtime/peer", "runtime"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	cfg := writeLicensingYAML(t, dir, `apache:
  - protocols/
  - runtime/peer/
agpl:
  - runtime/
exempt:
  - bin/
`)
	if vs := verifyEntriesExist(cfg, dir); len(vs) != 0 {
		t.Fatalf("want 0 violations, got %d: %+v", len(vs), vs)
	}
}
