// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMergeParams_JSONOnly(t *testing.T) {
	got, err := mergeParams(`{"a":1,"b":"x"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].(float64) != 1 || got["b"] != "x" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeParams_KVOnly(t *testing.T) {
	got, err := mergeParams("", RepeatedFlag{"count=3", "enabled=true", "name=foo"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"count": int64(3), "enabled": true, "name": "foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestMergeParams_KVOverridesJSON(t *testing.T) {
	got, err := mergeParams(`{"a":1,"b":2}`, RepeatedFlag{"b=99"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].(float64) != 1 {
		t.Fatalf("a clobbered: %+v", got)
	}
	if got["b"].(int64) != 99 {
		t.Fatalf("b not overridden by --param: %+v", got)
	}
}

func TestMergeParams_BadKV(t *testing.T) {
	if _, err := mergeParams("", RepeatedFlag{"missing-eq"}); err == nil {
		t.Fatal("want error for k=v without '='")
	}
	if _, err := mergeParams("", RepeatedFlag{"=novalue"}); err == nil {
		t.Fatal("want error for empty key")
	}
}

func TestResolveServiceBindings_Explicit(t *testing.T) {
	got, err := resolveServiceBindings(RepeatedFlag{"codegen=/usr/bin/cg", "fs=/bin/fs"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bindingSpec{
		"codegen": {Path: "/usr/bin/cg"},
		"fs":      {Path: "/bin/fs"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestResolveServiceBindings_BareWithAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAliasFile(t, filepath.Join(home, ".rimsky", "aliases.yml"), "aliases:\n  codegen: /opt/codegen\n")
	chdir(t, t.TempDir())

	got, err := resolveServiceBindings(RepeatedFlag{"codegen"})
	if err != nil {
		t.Fatal(err)
	}
	if got["codegen"].Path != "/opt/codegen" {
		t.Fatalf("bare name did not resolve via alias: %+v", got)
	}
}

func TestResolveServiceBindings_BareWithoutAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdir(t, t.TempDir())
	if _, err := resolveServiceBindings(RepeatedFlag{"nope"}); err == nil {
		t.Fatal("want error for bare name with no alias")
	}
}

func TestResolveServiceBindings_Empty(t *testing.T) {
	got, err := resolveServiceBindings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for no --service flags, got %+v", got)
	}
}

func TestRunRun_TemplateAndFileMutuallyExclusive(t *testing.T) {
	t.Setenv("RIMSKY_CONTROL_API", "http://127.0.0.1:0")
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	specPath := filepath.Join(t.TempDir(), "spec.yml")
	if err := os.WriteFile(specPath, []byte("name: x\nversion: \"1\"\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RunRun(context.Background(), []string{"--template", "foo", specPath}); got != 2 {
		t.Fatalf("exit %d, want 2", got)
	}
}

func writeAliasFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
