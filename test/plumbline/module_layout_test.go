// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestFoundationDoesNotReExportClaimProducerTypes(t *testing.T) {
	repoRoot := findRepoRoot(t)
	foundationRoot := filepath.Join(repoRoot, "lib", "foundation")

	allowed := map[string]bool{
		"lib/foundation/locks/interface.go:ClaimProducer=claimproducer.ClaimProducer": true,
	}

	found := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(foundationRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}

		importsByName := map[string]string{}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			name := imp.Name.String()
			if imp.Name == nil {
				parts := strings.Split(importPath, "/")
				name = parts[len(parts)-1]
			}
			importsByName[name] = importPath
		}

		relPath, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			t.Fatalf("rel %s: %v", path, rerr)
		}
		relPath = filepath.ToSlash(relPath)

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || typeSpec.Assign == token.NoPos {
					continue
				}
				sel, ok := typeSpec.Type.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok {
					continue
				}
				importPath, known := importsByName[pkgIdent.Name]
				if !known || !strings.HasSuffix(importPath, "/lib/protocols/claimproducer") {
					continue
				}
				key := relPath + ":" + typeSpec.Name.Name + "=" + pkgIdent.Name + "." + sel.Sel.Name
				found[key] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", foundationRoot, err)
	}

	for key := range found {
		if !allowed[key] {
			t.Errorf("unexpected re-export alias of a protocols type in lib/foundation: %s\n"+
				"lib/foundation must use lib/protocols types directly, not re-export them as local aliases", key)
		}
	}
	for key := range allowed {
		if !found[key] {
			t.Errorf("expected re-export alias missing: %s (lib/foundation/locks.ClaimProducer is depended on across lib/runtime and lib/control)", key)
		}
	}
}

// @decision: toplevel-dirs
func TestNoTopLevelCompatShimDirectories(t *testing.T) {
	repoRoot := findRepoRoot(t)

	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		t.Fatalf("read %s: %v", repoRoot, err)
	}
	shimNames := map[string]bool{"protocols": true, "foundation": true, "services": true}
	for _, e := range entries {
		if e.IsDir() && shimNames[e.Name()] {
			t.Errorf("top-level compat shim directory %q found at repo root; public import paths must carry the lib/ segment (lib/%s), not a root-level re-export shim", e.Name(), e.Name())
		}
	}

	expectedModules := map[string]string{
		".":              "github.com/rimsky-ai/rimsky-core",
		"lib/foundation": "github.com/rimsky-ai/rimsky-core/lib/foundation",
		"lib/protocols":  "github.com/rimsky-ai/rimsky-core/lib/protocols",
		"lib/services":   "github.com/rimsky-ai/rimsky-core/lib/services",
	}
	for rel, want := range expectedModules {
		goModPath := filepath.Join(repoRoot, filepath.FromSlash(rel), "go.mod")
		raw, rerr := os.ReadFile(goModPath)
		if rerr != nil {
			t.Fatalf("read %s: %v", goModPath, rerr)
		}
		firstLine := strings.SplitN(string(raw), "\n", 2)[0]
		got := strings.TrimSpace(strings.TrimPrefix(firstLine, "module"))
		if got != want {
			t.Errorf("%s: module directive = %q, want %q", goModPath, got, want)
		}
	}
}

func TestNoInternalOpsPackage(t *testing.T) {
	repoRoot := findRepoRoot(t)
	skipDirs := map[string]bool{
		".git":        true,
		".ok-planner": true,
		"vendor":      true,
		"bin":         true,
		"tmp":         true,
	}

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != repoRoot && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if d.Name() == "ops" && filepath.Base(filepath.Dir(path)) == "internal" {
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				rel = path
			}
			t.Errorf("found %s; internal/ops was deleted (not relocated) as dead weight "+
				"after the services split and must not exist", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoRoot, err)
	}
}

func TestNoStandaloneConformanceBinary(t *testing.T) {
	repoRoot := findRepoRoot(t)
	cmdRoot := filepath.Join(repoRoot, "cmd")

	entries, err := os.ReadDir(cmdRoot)
	if err != nil {
		t.Fatalf("read %s: %v", cmdRoot, err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.Contains(strings.ToLower(e.Name()), "conformance") {
			t.Errorf("found standalone conformance binary directory cmd/%s; conformance runners must be `rimsky conformance <protocol>` subcommands, not a separate binary", e.Name())
		}
	}

	mainSrc, err := os.ReadFile(filepath.Join(cmdRoot, "rimsky", "main.go"))
	if err != nil {
		t.Fatalf("read cmd/rimsky/main.go: %v", err)
	}
	if !strings.Contains(string(mainSrc), `case "conformance":`) {
		t.Errorf("cmd/rimsky/main.go no longer routes a %q subcommand; `rimsky conformance <protocol>` is the only sanctioned conformance runner surface", "conformance")
	}

	conformanceSrc, err := os.ReadFile(filepath.Join(cmdRoot, "rimsky", "conformance.go"))
	if err != nil {
		t.Fatalf("read cmd/rimsky/conformance.go: %v", err)
	}
	if !strings.Contains(string(conformanceSrc), "func dispatchConformance(") {
		t.Errorf("cmd/rimsky/conformance.go no longer defines dispatchConformance; the conformance subcommand implementation moved or was removed")
	}
}

func TestGeneratedProtoBindingsMatchSource(t *testing.T) {
	for _, tool := range []string{"protoc", "protoc-gen-go", "protoc-gen-go-grpc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH; cannot verify generated proto bindings are in sync with .proto sources", tool)
		}
	}

	repoRoot := findRepoRoot(t)
	protoDir := filepath.Join(repoRoot, "lib", "protocols", "proto", "v1")
	genDir := filepath.Join(protoDir, "gen")
	scratchDir := t.TempDir()

	protoFiles := []string{
		"executor.proto", "events.proto", "claim_producer.proto", "lifecycle.proto",
		"executor_observability.proto", "claim_producer_observability.proto",
		"data_processing.proto", "validation.proto", "publisher.proto", "host_agent.proto",
	}

	args := []string{
		"--go_out=" + scratchDir, "--go_opt=paths=source_relative",
		"--go-grpc_out=" + scratchDir, "--go-grpc_opt=paths=source_relative",
	}
	args = append(args, protoFiles...)

	cmd := exec.Command("protoc", args...)
	cmd.Dir = protoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("protoc regen failed: %v\n%s", err, out)
	}

	regenerated, err := filepath.Glob(filepath.Join(scratchDir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", scratchDir, err)
	}
	if len(regenerated) == 0 {
		t.Fatalf("protoc produced no output files in %s", scratchDir)
	}
	sort.Strings(regenerated)

	for _, freshPath := range regenerated {
		name := filepath.Base(freshPath)
		checkedInPath := filepath.Join(genDir, name)

		fresh, ferr := os.ReadFile(freshPath)
		if ferr != nil {
			t.Fatalf("read %s: %v", freshPath, ferr)
		}
		checkedIn, cerr := os.ReadFile(checkedInPath)
		if os.IsNotExist(cerr) {
			t.Errorf("protoc regeneration produced %s but no such file is checked in under lib/protocols/proto/v1/gen; gen/ is out of sync with .proto sources", name)
			continue
		}
		if cerr != nil {
			t.Fatalf("read %s: %v", checkedInPath, cerr)
		}
		if string(fresh) != string(checkedIn) {
			t.Errorf("lib/protocols/proto/v1/gen/%s differs from a fresh `make proto-gen` regeneration; "+
				"generated proto bindings must be regenerated from .proto sources, never hand- or string-edited", name)
		}
	}
}
