// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_test.go — table-driven coverage for the `compose run` flag
// parser (@decision: cli-verb / timeout-flag / progress-flags /
// service-spawn-flag / artifact-root-discovery) and unit coverage for
// the spawn-step alias-resolution gate (@decision: service-spawn-flag,
// the bare `--service <name>` form). Package-internal so the
// unexported flags struct and spawnServices helper are reachable.
package compose

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func TestRunComposeRun_FlagParse_HappyPath(t *testing.T) {
	flags, code := parseComposeRunFlags([]string{"./manifest.yml"})
	if code != 0 {
		t.Fatalf("parse returned exit %d, want 0", code)
	}
	if flags.manifestPath != "./manifest.yml" {
		t.Errorf("manifestPath = %q, want ./manifest.yml", flags.manifestPath)
	}
	if flags.timeout != 0 {
		t.Errorf("timeout = %v, want 0 (unbounded)", flags.timeout)
	}
	if flags.quiet || flags.verbose || flags.json {
		t.Errorf("progress flags should default to false, got quiet=%v verbose=%v json=%v",
			flags.quiet, flags.verbose, flags.json)
	}
	if len(flags.services) != 0 {
		t.Errorf("services should default to empty, got %v", flags.services)
	}
}

func TestRunComposeRun_FlagParse_WithFlags(t *testing.T) {
	args := []string{
		"--name", "foo",
		"--timeout", "30m",
		"--quiet",
		"./m.yml",
	}
	flags, code := parseComposeRunFlags(args)
	if code != 0 {
		t.Fatalf("parse returned exit %d, want 0", code)
	}
	if flags.name != "foo" {
		t.Errorf("name = %q, want foo", flags.name)
	}
	if flags.timeout != 30*time.Minute {
		t.Errorf("timeout = %v, want 30m", flags.timeout)
	}
	if !flags.quiet {
		t.Errorf("quiet should be true")
	}
	if flags.manifestPath != "./m.yml" {
		t.Errorf("manifestPath = %q, want ./m.yml", flags.manifestPath)
	}
}

func TestRunComposeRun_FlagParse_NoPositional(t *testing.T) {
	_, code := parseComposeRunFlags([]string{"--name", "foo"})
	if code != 2 {
		t.Errorf("parse without positional returned exit %d, want 2", code)
	}
}

func TestRunComposeRun_FlagParse_TooManyPositionals(t *testing.T) {
	_, code := parseComposeRunFlags([]string{"./a.yml", "./b.yml"})
	if code != 2 {
		t.Errorf("parse with two positionals returned exit %d, want 2", code)
	}
}

func TestRunComposeRun_FlagParse_QuietAndVerboseMutuallyExclusive(t *testing.T) {
	_, code := parseComposeRunFlags([]string{"--quiet", "--verbose", "./m.yml"})
	if code != 2 {
		t.Errorf("parse with --quiet+--verbose returned exit %d, want 2", code)
	}
}

func TestRunComposeRun_FlagParse_RepeatableService(t *testing.T) {
	args := []string{
		"--service", "foo=/bin/x",
		"--service", "bar",
		"./m.yml",
	}
	flags, code := parseComposeRunFlags(args)
	if code != 0 {
		t.Fatalf("parse returned exit %d, want 0", code)
	}
	if len(flags.services) != 2 {
		t.Fatalf("services len = %d, want 2: %v", len(flags.services), flags.services)
	}
	if flags.services[0] != "foo=/bin/x" {
		t.Errorf("services[0] = %q, want foo=/bin/x", flags.services[0])
	}
	if flags.services[1] != "bar" {
		t.Errorf("services[1] = %q, want bar", flags.services[1])
	}
}

func TestRunComposeRun_FlagParse_WorkdirAndJSON(t *testing.T) {
	args := []string{
		"--workdir", "/tmp/explicit",
		"--json",
		"./m.yml",
	}
	flags, code := parseComposeRunFlags(args)
	if code != 0 {
		t.Fatalf("parse returned exit %d, want 0", code)
	}
	if flags.workdir != "/tmp/explicit" {
		t.Errorf("workdir = %q, want /tmp/explicit", flags.workdir)
	}
	if !flags.json {
		t.Errorf("json should be true")
	}
}

func TestRunComposeRun_FlagParse_EmptyManifestPath(t *testing.T) {
	_, code := parseComposeRunFlags([]string{"   "})
	if code != 2 {
		t.Errorf("parse with whitespace-only manifest returned exit %d, want 2", code)
	}
}

func TestRunComposeRun_FlagParse_UnknownFlag(t *testing.T) {
	_, code := parseComposeRunFlags([]string{"--bogus", "./m.yml"})
	if code != 2 {
		t.Errorf("parse with unknown flag returned exit %d, want 2", code)
	}
}

// quietLogger returns a slog.Logger that discards output. Used in
// spawnServices tests so the spawn-info log line doesn't clutter
// `go test -v`.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// chdirT chdirs into dir for the duration of the test and restores
// the prior cwd on cleanup. Mirrors cli.chdir from run_flags_test.go;
// duplicated here because that helper is in the cli_test package and
// is unexported.
func chdirT(t *testing.T, dir string) {
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

// writeAliasFileT writes an aliases.yml file at path, creating parent
// dirs. Mirrors cli.writeAliasFile from run_flags_test.go.
func writeAliasFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSpawnServices_BareNameNoAlias asserts that a bare `--service
// <name>` with no alias file returns the same diagnostic shape as
// cli.resolveServiceBindings: a "no alias defined" error naming the
// service and pointing the operator at the explicit form. The verb
// must not silently swallow the missing alias or reach SpawnService.
//
// @decision: service-spawn-flag
func TestSpawnServices_BareNameNoAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no global aliases
	chdirT(t, t.TempDir())        // no project-local aliases

	_, _, err := spawnServices(context.Background(), cli.RepeatedFlag{"nope"}, quietLogger())
	if err == nil {
		t.Fatal("spawnServices: expected error for bare name with no alias, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no alias defined") {
		t.Errorf("error %q should mention `no alias defined` (mirrors cli.resolveServiceBindings)", msg)
	}
	if !strings.Contains(msg, "nope") {
		t.Errorf("error %q should name the unresolved service `nope`", msg)
	}
}

// TestSpawnServices_BareNameAliasResolvesPastGate asserts that a bare
// `--service <name>` with an alias defined gets past the resolution
// gate. We confirm this by supplying an alias path to a binary that
// does not exist on disk and observing that the resulting error is
// the spawn-stage error (the binary couldn't be exec'd), NOT the
// resolution-stage "no alias defined" / "bare-name aliases
// unsupported" / "path is empty" error. The contrast proves the
// alias-loader integration works end-to-end.
//
// @decision: service-spawn-flag
func TestSpawnServices_BareNameAliasResolvesPastGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bogusBinary := filepath.Join(t.TempDir(), "definitely-not-a-binary")
	writeAliasFileT(t,
		filepath.Join(home, ".rimsky", "aliases.yml"),
		"aliases:\n  codegen: "+bogusBinary+"\n",
	)
	chdirT(t, t.TempDir()) // no project-local aliases overlay

	_, _, err := spawnServices(context.Background(), cli.RepeatedFlag{"codegen"}, quietLogger())
	if err == nil {
		t.Fatal("spawnServices: expected spawn-stage error for non-existent binary, got nil")
	}
	msg := err.Error()
	// Resolution-stage diagnostics must NOT appear — proving the
	// alias loader resolved the bare name and the helper progressed
	// to the spawn stage.
	for _, blocked := range []string{
		"no alias defined",
		"bare-name aliases unsupported",
		"path is empty",
		"service name is empty",
	} {
		if strings.Contains(msg, blocked) {
			t.Errorf("error %q must not contain resolution-stage diagnostic %q — alias resolution should have progressed to the spawn stage", msg, blocked)
		}
	}
}

// TestSpawnServices_ProjectLocalAliasOverlaysGlobal asserts the layer
// ordering — project-local `./.rimsky/aliases.yml` overlays the
// global `~/.rimsky/aliases.yml` — matches cli.LoadServiceAliases.
// Observed via the spawn-stage error referencing the project-local
// path rather than the global path.
//
// @decision: service-spawn-flag
func TestSpawnServices_ProjectLocalAliasOverlaysGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalBin := filepath.Join(t.TempDir(), "global-bin-does-not-exist")
	projBin := filepath.Join(t.TempDir(), "project-bin-does-not-exist")
	writeAliasFileT(t,
		filepath.Join(home, ".rimsky", "aliases.yml"),
		"aliases:\n  codegen: "+globalBin+"\n",
	)
	proj := t.TempDir()
	chdirT(t, proj)
	writeAliasFileT(t,
		filepath.Join(proj, ".rimsky", "aliases.yml"),
		"aliases:\n  codegen: "+projBin+"\n",
	)

	_, _, err := spawnServices(context.Background(), cli.RepeatedFlag{"codegen"}, quietLogger())
	if err == nil {
		t.Fatal("spawnServices: expected spawn-stage error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, projBin) {
		t.Errorf("error %q should reference project-local path %q (overlay winner)", msg, projBin)
	}
	if strings.Contains(msg, globalBin) {
		t.Errorf("error %q should NOT reference global path %q (project-local should override)", msg, globalBin)
	}
}

// TestSpawnServices_ExplicitEmptyPath asserts the cosmetic distinction
// between `--service foo=` (operator typed `=` but supplied no path)
// and bare `--service foo` (no `=`, alias-resolution form). The
// explicit-with-empty-path form is always an error regardless of
// aliases — matches cli.resolveServiceBindings.
//
// @decision: service-spawn-flag
func TestSpawnServices_ExplicitEmptyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Define a foo alias to prove that even WITH an alias present,
	// the explicit `foo=` form still errors (it is not bare).
	writeAliasFileT(t,
		filepath.Join(home, ".rimsky", "aliases.yml"),
		"aliases:\n  foo: /some/path\n",
	)
	chdirT(t, t.TempDir())

	_, _, err := spawnServices(context.Background(), cli.RepeatedFlag{"foo="}, quietLogger())
	if err == nil {
		t.Fatal("spawnServices: expected error for explicit-empty-path form, got nil")
	}
	if !strings.Contains(err.Error(), "path is empty") {
		t.Errorf("error %q should mention `path is empty`", err.Error())
	}
}

// TestSpawnServices_EmptyNameBareForm asserts that an empty raw
// value (no name, no `=`) is caught before any alias work.
//
// @decision: service-spawn-flag
func TestSpawnServices_EmptyNameBareForm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirT(t, t.TempDir())

	_, _, err := spawnServices(context.Background(), cli.RepeatedFlag{"   "}, quietLogger())
	if err == nil {
		t.Fatal("spawnServices: expected error for empty-name form, got nil")
	}
	if !strings.Contains(err.Error(), "service name is empty") {
		t.Errorf("error %q should mention `service name is empty`", err.Error())
	}
}
