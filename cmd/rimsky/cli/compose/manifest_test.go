// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rimsky-compose.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadManifest_HappyPath(t *testing.T) {
	path := writeManifest(t, `project: ingest-pipeline
context: staging
templates:
  - path: ./graphs/a.yml
    tag: a@1.0
instances:
  - template: a@1.0
    name: hello
    restart: on_failure
`)
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Project != "ingest-pipeline" {
		t.Errorf("project: %q", m.Project)
	}
	if m.PrefixedTag("a@1.0") != "compose:ingest-pipeline:a@1.0" {
		t.Errorf("prefixed: %q", m.PrefixedTag("a@1.0"))
	}
}

func TestValidate_RequiredProject(t *testing.T) {
	m := &Manifest{}
	if err := m.Validate(); err == nil {
		t.Fatal("want error")
	}
}

func TestValidate_BadProjectName(t *testing.T) {
	m := &Manifest{Project: "Bad Project"}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_TemplateReservedPrefix(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "compose:p:foo"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "reserved prefix") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_DuplicatePath(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "a"},
			{Path: "x.yml", Tag: "b"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_DuplicateInstanceName(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "a"},
		},
		Instances: []InstanceRef{
			{Template: "a", Name: "n"},
			{Template: "a", Name: "n"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_BadInstanceName(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "a"},
		},
		Instances: []InstanceRef{
			{Template: "a", Name: "Bad Name"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "instances[0].name") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_UnknownTemplateRef(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "a"},
		},
		Instances: []InstanceRef{
			{Template: "missing", Name: "n"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "neither a manifest tag nor a hash") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_HashTemplateRef(t *testing.T) {
	hash := "sha256-" + strings.Repeat("a", 64)
	m := &Manifest{
		Project: "p",
		Instances: []InstanceRef{
			{Template: hash, Name: "n"},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_BadRestart(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "a"},
		},
		Instances: []InstanceRef{
			{Template: "a", Name: "n", Restart: "bogus"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "restart") {
		t.Errorf("got %v", err)
	}
}

func TestValidate_MultiError(t *testing.T) {
	m := &Manifest{
		Project: "Bad Project",
		Templates: []TemplateRef{
			{Path: "", Tag: "a"},
		},
		Instances: []InstanceRef{
			{Template: "missing", Name: "Bad"},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	// errors.Join produces a multi-error; the wrapped slice is visible.
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected a joined error, got %T", err)
	}
	if len(joined.Unwrap()) < 3 {
		t.Errorf("want >=3 errors, got %d: %v", len(joined.Unwrap()), err)
	}
}

func TestResolveTemplateRef(t *testing.T) {
	m := &Manifest{Project: "p"}
	if r, k := m.ResolveTemplateRef("a@1.0"); r != "compose:p:a@1.0" || k != "tag" {
		t.Errorf("got %q,%q", r, k)
	}
	hash := "sha256-" + strings.Repeat("0", 64)
	if r, k := m.ResolveTemplateRef(hash); r != hash || k != "hash" {
		t.Errorf("got %q,%q", r, k)
	}
}
