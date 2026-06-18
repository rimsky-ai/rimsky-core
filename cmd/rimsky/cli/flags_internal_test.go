// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"flag"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *bool) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		tmpl := fs.String("template", "", "")
		force := fs.Bool("force", false, "")
		return fs, tmpl, force
	}

	t.Run("flag after positional (space form)", func(t *testing.T) {
		fs, tmpl, _ := newFS()
		if err := parseInterspersed(fs, []string{"v1", "--template", "abc"}); err != nil {
			t.Fatalf("err: %v", err)
		}
		if *tmpl != "abc" {
			t.Errorf("template = %q, want abc", *tmpl)
		}
		if args := fs.Args(); len(args) != 1 || args[0] != "v1" {
			t.Errorf("positionals = %v, want [v1]", args)
		}
	})

	t.Run("flag after positional (equals form)", func(t *testing.T) {
		fs, tmpl, _ := newFS()
		if err := parseInterspersed(fs, []string{"v1", "--template=abc"}); err != nil {
			t.Fatalf("err: %v", err)
		}
		if *tmpl != "abc" {
			t.Errorf("template = %q, want abc", *tmpl)
		}
	})

	t.Run("boolean flag does not swallow following positional", func(t *testing.T) {
		fs, _, force := newFS()
		if err := parseInterspersed(fs, []string{"v1", "--force"}); err != nil {
			t.Fatalf("err: %v", err)
		}
		if !*force {
			t.Errorf("force = false, want true")
		}
		if args := fs.Args(); len(args) != 1 || args[0] != "v1" {
			t.Errorf("positionals = %v, want [v1]", args)
		}
	})

	t.Run("double-dash terminates flag interpretation", func(t *testing.T) {
		fs, tmpl, _ := newFS()
		if err := parseInterspersed(fs, []string{"v1", "--", "--template", "literal"}); err != nil {
			t.Fatalf("err: %v", err)
		}
		if *tmpl != "" {
			t.Errorf("template = %q, want empty (after --)", *tmpl)
		}
		if args := fs.Args(); len(args) != 3 {
			t.Errorf("positionals = %v, want 3 (v1 --template literal)", args)
		}
	})

	t.Run("flags-first ordering unchanged", func(t *testing.T) {
		fs, tmpl, _ := newFS()
		if err := parseInterspersed(fs, []string{"--template", "abc", "v1"}); err != nil {
			t.Fatalf("err: %v", err)
		}
		if *tmpl != "abc" {
			t.Errorf("template = %q, want abc", *tmpl)
		}
		if args := fs.Args(); len(args) != 1 || args[0] != "v1" {
			t.Errorf("positionals = %v, want [v1]", args)
		}
	})

	t.Run("unknown flag still errors", func(t *testing.T) {
		fs, _, _ := newFS()
		if err := parseInterspersed(fs, []string{"v1", "--bogus", "x"}); err == nil {
			t.Errorf("expected error for unknown flag, got nil")
		}
	})
}
