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
	// @constraint: errors.Join produces a multi-error; the wrapped slice is visible via the Unwrap() []error interface.
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected a joined error, got %T", err)
	}
	if len(joined.Unwrap()) < 3 {
		t.Errorf("want >=3 errors, got %d: %v", len(joined.Unwrap()), err)
	}
}

func TestManifest_ValidExecutorsBlock(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Templates: []TemplateRef{
			{Path: "x.yml", Tag: "a"},
		},
		Executors: map[string]ManifestExecutorEntry{
			"stub": {
				Transport:             "grpc",
				Endpoint:              "127.0.0.1:9091",
				TLS:                   "off",
				ObservabilityEndpoint: "127.0.0.1:9092",
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid manifest, got %v", err)
	}
}

func TestManifest_ExecutorTransportInvalid(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Executors: map[string]ManifestExecutorEntry{
			"stub": {
				Transport: "foo",
				Endpoint:  "127.0.0.1:9091",
			},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Errorf("got %v", err)
	}
}

func TestManifest_ClaimProducerMissingWriteSemantics(t *testing.T) {
	m := &Manifest{
		Project: "p",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint: "127.0.0.1:9095",
			},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "write_semantics_allowed: required") {
		t.Errorf("got %v", err)
	}
}

func TestManifest_ServiceNameInvalid(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Executors: map[string]ManifestExecutorEntry{
			"Foo": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9091",
			},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "service name") {
		t.Errorf("got %v", err)
	}
}

func TestManifest_ClaimProducerValid(t *testing.T) {
	m := &Manifest{
		Project: "p",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint:              "127.0.0.1:9095",
				WriteSemanticsAllowed: []string{"sync"},
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid manifest, got %v", err)
	}
}

func TestManifest_ClaimProducerBadProtocol(t *testing.T) {
	m := &Manifest{
		Project: "p",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint:              "127.0.0.1:9095",
				WriteSemanticsAllowed: []string{"sync"},
				Protocols:             []string{"bogus-protocol"},
			},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "is not a known protocol") {
		t.Errorf("got %v", err)
	}
}

func TestManifest_ExecutorBadProtocol(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Executors: map[string]ManifestExecutorEntry{
			"stub": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9091",
				Protocols: []string{"bogus-protocol"},
			},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "is not a known protocol") {
		t.Errorf("got %v", err)
	}
}

func TestManifest_ExecutorMultiProtocolValid(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Executors: map[string]ManifestExecutorEntry{
			"multi": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9091",
				Protocols: []string{"executor", "lifecycle_subscriber"},
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid manifest, got %v", err)
	}
}

func TestManifest_ClaimProducerBadWriteSemantics(t *testing.T) {
	m := &Manifest{
		Project: "p",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint:              "127.0.0.1:9095",
				WriteSemanticsAllowed: []string{"bogus"},
			},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("got %v", err)
	}
}

func TestSiblingRimskyYMLPath_Present(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(manifestPath, []byte("project: p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	siblingPath := filepath.Join(dir, "rimsky.yml")
	if err := os.WriteFile(siblingPath, []byte("persistence:\n  driver: sqlite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SiblingRimskyYMLPath(manifestPath)
	if err != nil {
		t.Fatalf("SiblingRimskyYMLPath: %v", err)
	}
	if got == "" {
		t.Fatalf("expected sibling path, got empty")
	}
	// @constraint: the returned path resolves to the same file as the sibling we just wrote.
	gotAbs, _ := filepath.Abs(got)
	wantAbs, _ := filepath.Abs(siblingPath)
	if gotAbs != wantAbs {
		t.Errorf("got %q, want %q", gotAbs, wantAbs)
	}
}

func TestSiblingRimskyYMLPath_Absent(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(manifestPath, []byte("project: p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SiblingRimskyYMLPath(manifestPath)
	if err != nil {
		t.Fatalf("SiblingRimskyYMLPath: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty (no sibling), got %q", got)
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
