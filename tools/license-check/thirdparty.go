// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type thirdPartyLicenses struct {
	permitted map[string]bool
	modules   []moduleLicense
}

type moduleLicense struct {
	path    string
	license string
}

func newThirdPartyLicenses(permitted []string, modules map[string]string) (thirdPartyLicenses, error) {
	tp := thirdPartyLicenses{permitted: map[string]bool{}}
	for _, id := range permitted {
		tp.permitted[id] = true
	}
	for path, license := range modules {
		if strings.TrimSpace(license) == "" {
			return thirdPartyLicenses{}, fmt.Errorf("third_party.modules[%s] has no license", path)
		}
		tp.modules = append(tp.modules, moduleLicense{path: path, license: license})
	}
	sort.SliceStable(tp.modules, func(i, j int) bool { return len(tp.modules[i].path) > len(tp.modules[j].path) })
	return tp, nil
}

func (tp thirdPartyLicenses) lookup(importPath string) (moduleLicense, bool) {
	for _, m := range tp.modules {
		if importPath == m.path || strings.HasPrefix(importPath, m.path+"/") {
			return m, true
		}
	}
	return moduleLicense{}, false
}

// @decision: licensing-enforced-by-license-lint
func (tp thirdPartyLicenses) permits(expression string) bool {
	for _, term := range strings.Fields(expression) {
		switch term {
		case "AND", "OR":
			continue
		}
		if !tp.permitted[strings.Trim(term, "()")] {
			return false
		}
	}
	return true
}

func isStdlibImport(importPath string) bool {
	head, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(head, ".")
}

// @decision: licensing-enforced-by-license-lint
func verifyThirdPartyImport(relPath, importPath string, tp thirdPartyLicenses) *violation {
	m, ok := tp.lookup(importPath)
	if !ok {
		return &violation{
			path: relPath,
			message: "Apache file imports unclassified third-party module: " + importPath +
				" — classify its module in licensing.yml third_party.modules before it can ship",
		}
	}
	if !tp.permits(m.license) {
		return &violation{
			path:    relPath,
			message: fmt.Sprintf("Apache file imports %s under a license the permissive surface cannot carry: %s (%s)", importPath, m.license, m.path),
		}
	}
	return nil
}

// @decision: licensing-enforced-by-license-lint
func verifyApacheClosure(cfg *licensingConfig, root string) []violation {
	var out []violation
	for _, prefix := range cfg.apachePrefixes {
		dir := filepath.Join(root, filepath.FromSlash(prefix))
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			continue
		}
		modules, err := buildClosureModules(dir)
		if err != nil {
			out = append(out, violation{path: prefix, message: "enumerate dependency closure: " + err.Error()})
			continue
		}
		for _, mod := range modules {
			if strings.HasPrefix(mod, "github.com/rimsky-ai/rimsky-core") {
				continue
			}
			m, ok := cfg.thirdParty.lookup(mod)
			if !ok {
				out = append(out, violation{
					path: prefix,
					message: "dependency closure contains unclassified third-party module: " + mod +
						" — classify it in licensing.yml third_party.modules",
				})
				continue
			}
			if !cfg.thirdParty.permits(m.license) {
				out = append(out, violation{
					path:    prefix,
					message: fmt.Sprintf("dependency closure contains %s under a license the permissive surface cannot carry: %s", mod, m.license),
				})
			}
		}
	}
	return out
}

func buildClosureModules(moduleDir string) ([]string, error) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./...")
	cmd.Dir = moduleDir
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps in %s: %w", moduleDir, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(stdout), "\n") {
		mod := strings.TrimSpace(line)
		if mod == "" || seen[mod] {
			continue
		}
		seen[mod] = true
		out = append(out, mod)
	}
	sort.Strings(out)
	return out, nil
}
