// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/ports"
)

func bundledServiceDirs(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	for _, family := range shippedServiceFamilies {
		familyRoot := filepath.Join(root, "lib", "services", family)
		entries, err := os.ReadDir(familyRoot)
		if err != nil {
			t.Fatalf("read %s: %v", familyRoot, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			svcRoot := filepath.Join(familyRoot, e.Name())
			if !strings.Contains(serviceTreeSources(t, svcRoot), "func main()") {
				continue
			}
			rel, err := filepath.Rel(root, svcRoot)
			if err != nil {
				t.Fatalf("rel %s: %v", svcRoot, err)
			}
			dirs = append(dirs, rel)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func declaredDefaultPorts(t *testing.T, serviceRoot string) map[string]int {
	t.Helper()
	found := map[string]int{}
	err := filepath.WalkDir(serviceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "default") || !strings.HasSuffix(name.Name, "Port") {
						continue
					}
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.INT {
						t.Fatalf("%s: %s must be declared as a plain integer literal so the port audit can read it", path, name.Name)
					}
					n, cerr := strconv.Atoi(lit.Value)
					if cerr != nil {
						return cerr
					}
					found[name.Name] = n
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", serviceRoot, err)
	}
	return found
}

var portsBlockBounds = map[string]bool{
	"CoreBlockFirst":    true,
	"CoreBlockLast":     true,
	"BundledBlockFirst": true,
	"BundledBlockLast":  true,
}

// @decision: default-port-allocation
func TestEveryCoreDefaultDeclaredInThePortsPackageIsEnumeratedByCoreDefaults(t *testing.T) {
	root := findRepoRoot(t)
	declared := exportedIntConsts(t, filepath.Join(root, "lib", "foundation", "ports"))
	if len(declared) == 0 {
		t.Fatalf("the ports package declares no exported integer constants — the core half of the port population is gone")
	}

	enumerated := map[int]string{}
	for name, port := range ports.CoreDefaults() {
		enumerated[port] = name
	}

	for name, port := range declared {
		if portsBlockBounds[name] {
			continue
		}
		if _, ok := enumerated[port]; !ok {
			t.Errorf("ports.%s = %d is a core default the ports package declares but CoreDefaults() does not enumerate, so no fitness check ever sees it", name, port)
		}
	}

	nonBounds := 0
	for name := range declared {
		if !portsBlockBounds[name] {
			nonBounds++
		}
	}
	if len(ports.CoreDefaults()) != nonBounds {
		t.Errorf("CoreDefaults() enumerates %d listeners but the ports package declares %d core defaults; the map names a port no constant declares",
			len(ports.CoreDefaults()), nonBounds)
	}
}

func exportedIntConsts(t *testing.T, pkgDir string) map[string]int {
	t.Helper()
	found := map[string]int{}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, e.Name())
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !name.IsExported() || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.INT {
						continue
					}
					n, cerr := strconv.Atoi(lit.Value)
					if cerr != nil {
						t.Fatalf("%s: %s = %s is not a port number", path, name.Name, lit.Value)
					}
					found[name.Name] = n
				}
			}
		}
	}
	return found
}

// @decision: default-port-allocation
func TestEveryShippedDefaultPortSitsInItsBlockAndCollidesWithNothing(t *testing.T) {
	root := findRepoRoot(t)
	owner := map[int]string{}

	claim := func(port int, site string) {
		if prev, taken := owner[port]; taken {
			t.Errorf("port %d is the shipped default for both %s and %s; no two shipped defaults may coincide", port, prev, site)
			return
		}
		owner[port] = site
	}

	for name, port := range ports.CoreDefaults() {
		site := "core listener " + name
		if port < ports.CoreBlockFirst || port > ports.CoreBlockLast {
			t.Errorf("%s defaults to %d, outside the core block %d-%d", site, port, ports.CoreBlockFirst, ports.CoreBlockLast)
		}
		claim(port, site)
	}

	dirs := bundledServiceDirs(t, root)
	if len(dirs) != 11 {
		t.Fatalf("bundled services = %d, want 11: %v", len(dirs), dirs)
	}

	portless := []string{}
	for _, dir := range dirs {
		declared := declaredDefaultPorts(t, filepath.Join(root, dir))
		if len(declared) == 0 {
			portless = append(portless, dir)
			continue
		}
		for constName, port := range declared {
			site := dir + " " + constName
			if port < ports.BundledBlockFirst || port > ports.BundledBlockLast {
				t.Errorf("%s defaults to %d, outside the bundled-service block %d-%d", site, port, ports.BundledBlockFirst, ports.BundledBlockLast)
			}
			claim(port, site)
		}
	}

	if len(portless) != 1 || portless[0] != filepath.Join("lib", "services", "subscribers", "openlineage") {
		t.Errorf("openlineage is the only bundled service that opens no listener, so it is the only one declaring no default port; got %v", portless)
	}
}

func dockerfileExposedPorts(t *testing.T, serviceRoot string) []int {
	t.Helper()
	var exposed []int
	entries, err := os.ReadDir(serviceRoot)
	if err != nil {
		t.Fatalf("read %s: %v", serviceRoot, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "Dockerfile") {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(serviceRoot, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "EXPOSE ") {
				continue
			}
			for _, field := range strings.Fields(strings.TrimPrefix(line, "EXPOSE ")) {
				n, cerr := strconv.Atoi(field)
				if cerr != nil {
					t.Fatalf("%s: EXPOSE names %q, which is not a port number", e.Name(), field)
				}
				exposed = append(exposed, n)
			}
		}
	}
	sort.Ints(exposed)
	return exposed
}

// @concept: service
// @decision: default-port-allocation
func TestEveryBundledImageDeclaresExactlyThePortsItsBinaryOpens(t *testing.T) {
	root := findRepoRoot(t)

	for _, dir := range bundledServiceDirs(t, root) {
		serviceRoot := filepath.Join(root, dir)

		var want []int
		for _, port := range declaredDefaultPorts(t, serviceRoot) {
			want = append(want, port)
		}
		sort.Ints(want)

		got := dockerfileExposedPorts(t, serviceRoot)
		if len(want) != len(got) {
			t.Errorf("%s: image declares ports %v, binary opens %v at its built-in defaults", dir, got, want)
			continue
		}
		for i := range want {
			if want[i] != got[i] {
				t.Errorf("%s: image declares ports %v, binary opens %v at its built-in defaults", dir, got, want)
				break
			}
		}
	}
}
