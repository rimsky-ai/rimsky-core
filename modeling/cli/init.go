// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// init.go — `rimsky-cli init [<dir>] [--force]`. Scaffolds a starter
// project from the embedded reference assets.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/fallguy/rimsky/modeling/cli/embedded"
)

// projectNamePattern is the spec §2.2 project-name regex.
var projectNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// sanitizeProjectName produces a project: identifier from a directory
// basename. Lowercases letters and replaces every non-conforming
// character with `-`. Leading non-letters are replaced with `p`.
func sanitizeProjectName(s string) string {
	if s == "" {
		return "rimsky"
	}
	var b strings.Builder
	for i, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteRune('p')
			}
			b.WriteRune(r)
		case r == '-':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
		if b.Len() >= 63 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || !projectNamePattern.MatchString(out) {
		// Last-ditch fallback: prefix with `p` until the regex passes.
		out = "p" + out
		if len(out) > 63 {
			out = out[:63]
		}
	}
	return out
}

// RunInit implements the `init` verb. ctx is unused but accepted for
// dispatcher symmetry.
func RunInit(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	var force bool
	fs.BoolVar(&force, "force", false, "overwrite existing files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	target := "."
	if fs.NArg() == 1 {
		target = fs.Arg(0)
	} else if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli init [<directory>] [--force]")
		return 2
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// `init <dir>` reads as "scaffold a *new* project here" (cargo /
	// git semantics). Auto-create the directory if it doesn't exist;
	// a typo creates an easily-removed empty dir, while requiring an
	// explicit mkdir adds friction to the common path.
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	project := sanitizeProjectName(filepath.Base(abs))

	created, err := scaffold(abs, project, force)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := ensureGitignoreEntry(abs, "/.rimsky/"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "scaffolded rimsky project at %s\n", abs)
	for _, p := range created {
		fmt.Fprintf(os.Stdout, "  + %s\n", p)
	}
	return 0
}

func scaffold(target, project string, force bool) ([]string, error) {
	created := []string{}

	// Walk the embedded FS and write each file to the target. The
	// rimsky-compose.yml.tmpl is rendered through text/template; all
	// other files are copied verbatim.
	err := fs.WalkDir(embedded.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		dst := filepath.Join(target, path)
		if path == "rimsky-compose.yml.tmpl" {
			dst = filepath.Join(target, "rimsky-compose.yml")
		}
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !force {
			if _, err := os.Stat(dst); err == nil {
				return fmt.Errorf("%s already exists; pass --force to overwrite", dst)
			}
		}
		raw, err := fs.ReadFile(embedded.FS, path)
		if err != nil {
			return err
		}
		var content []byte
		if path == "rimsky-compose.yml.tmpl" {
			tmpl, err := template.New("compose").Parse(string(raw))
			if err != nil {
				return err
			}
			var sb strings.Builder
			if err := tmpl.Execute(&sb, map[string]string{"Project": project}); err != nil {
				return err
			}
			content = []byte(sb.String())
		} else {
			content = raw
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return err
		}
		created = append(created, dst)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(target, ".rimsky"), 0o755); err != nil {
		return nil, err
	}
	return created, nil
}

// ensureGitignoreEntry creates or appends to .gitignore so entry is on
// its own line.
func ensureGitignoreEntry(target, entry string) error {
	path := filepath.Join(target, ".gitignore")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	out := string(raw)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += entry + "\n"
	return os.WriteFile(path, []byte(out), 0o644)
}
