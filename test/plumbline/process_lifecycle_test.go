// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const roleBootstrapDelegate = "roleboot.Main("

var coreNonMainEntrypoints = []string{
	"cmd/internal/roleboot/roleboot.go",
	"cmd/rimsky/cli/daemon.go",
	"cmd/rimsky/cli/compose/run.go",
	"cmd/rimsky/cli/compose/template_run.go",
}

func coreBinaryMainsFound(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join(root, "cmd"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if !strings.Contains(string(src), "func main()") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		found = append(found, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk cmd: %v", err)
	}
	sort.Strings(found)
	return found
}

func coreProcessEntrypoints(t *testing.T, root string) []string {
	t.Helper()
	found := coreBinaryMainsFound(t, root)
	if len(found) == 0 {
		t.Fatal("the walk over cmd/ found no binary, so the core half of this audit enumerates nothing")
	}
	return append(found, coreNonMainEntrypoints...)
}

func bundledServiceEntrypoints(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	for _, family := range shippedServiceFamilies {
		familyRoot := filepath.Join(root, "lib", "services", family)
		err := filepath.WalkDir(familyRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if !strings.Contains(string(src), "func main()") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			found = append(found, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", familyRoot, err)
		}
	}
	sort.Strings(found)
	return found
}

// @decision: logging-slog-only
// @decision: operator-env-namespaced-per-service
func TestEveryProcessEntrypointTakesItsLogLevelFromTheSharedVariable(t *testing.T) {
	root := findRepoRoot(t)

	bundled := bundledServiceEntrypoints(t, root)
	if len(bundled) != 11 {
		t.Fatalf("bundled-service entrypoints = %d, want 11 (one per shipped service image): %v", len(bundled), bundled)
	}

	for _, rel := range append(coreProcessEntrypoints(t, root), bundled...) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(src)
		if strings.Contains(text, roleBootstrapDelegate) {
			continue
		}
		if !strings.Contains(text, "serverkit.NewJSONLogger") {
			t.Errorf("%s installs no handler from serverkit.NewJSONLogger and delegates to no entrypoint that does; an operator's RIMSKY_LOG_LEVEL cannot reach this process", rel)
		}
		for _, banned := range []string{"slog.NewJSONHandler(", "slog.NewTextHandler("} {
			if strings.Contains(text, banned) {
				t.Errorf("%s builds its own handler with %s; build it with serverkit.NewJSONLogger so the shared level reaches it", rel, banned)
			}
		}
	}
}

// @decision: graceful-shutdown
func TestOnlyTheSharedHelperInstallsASignalHandler(t *testing.T) {
	root := findRepoRoot(t)
	helper := filepath.Join("lib", "protocols", "serverkit", "shutdown.go")

	var offenders []string
	for _, tree := range []string{"cmd", "lib"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "test" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			if rel == helper {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if strings.Contains(string(src), "signal.Notify") {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these files install their own signal handler instead of calling the shared helper in %s, so a second signal escalates only in the processes that call it: %v", helper, offenders)
	}
}

const secondSignalEscalator = "serverkit.InstallSecondSignalHardExit("

// @decision: graceful-shutdown
func TestEverySignalNotifierAlsoInstallsTheSecondSignalEscalation(t *testing.T) {
	root := findRepoRoot(t)
	helperDir := filepath.Join("lib", "protocols", "serverkit")

	notifies := map[string]bool{}
	escalates := map[string]bool{}
	for _, tree := range []string{"cmd", "lib"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "test" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			if strings.HasPrefix(rel, helperDir) {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			text := string(src)
			pkg := filepath.Dir(rel)
			if strings.Contains(text, "serverkit.NotifyShutdownSignals") {
				notifies[pkg] = true
			}
			if strings.Contains(text, secondSignalEscalator) {
				escalates[pkg] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}

	if len(notifies) == 0 {
		t.Fatal("no package outside the helper takes a signal channel of its own, so this audit checks nothing")
	}
	var offenders []string
	for pkg := range notifies {
		if !escalates[pkg] {
			offenders = append(offenders, pkg)
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these packages take their own signal channel but never call %s, so an operator's second interrupt is swallowed: %v", secondSignalEscalator, offenders)
	}
}
