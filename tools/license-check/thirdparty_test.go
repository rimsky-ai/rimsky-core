// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"strings"
	"testing"
)

func makeThirdPartyFixture(t *testing.T) thirdPartyLicenses {
	t.Helper()
	tp, err := newThirdPartyLicenses(
		[]string{"Apache-2.0", "BSD-3-Clause", "MIT"},
		map[string]string{
			"github.com/google/uuid":       "BSD-3-Clause",
			"google.golang.org/grpc":       "Apache-2.0",
			"gopkg.in/yaml.v3":             "MIT AND Apache-2.0",
			"example.com/copyleft":         "GPL-3.0-only",
			"example.com/copyleft/subpath": "GPL-3.0-only",
		})
	if err != nil {
		t.Fatalf("newThirdPartyLicenses: %v", err)
	}
	return tp
}

// @decision: licensing-enforced-by-license-lint
func TestUnclassifiedThirdPartyModuleFailsClosed(t *testing.T) {
	tp := makeThirdPartyFixture(t)
	v := verifyThirdPartyImport("lib/protocols/x.go", "github.com/never/classified/pkg", tp)
	if v == nil {
		t.Fatal("an unclassified third-party module must be a violation; a silent pass is how contamination enters unnoticed")
	}
	if !strings.Contains(v.message, "unclassified") {
		t.Errorf("violation must name the gap, got: %s", v.message)
	}
}

// @decision: licensing-enforced-by-license-lint
func TestCopyleftThirdPartyModuleIsRejected(t *testing.T) {
	tp := makeThirdPartyFixture(t)
	v := verifyThirdPartyImport("lib/protocols/x.go", "example.com/copyleft/subpath/inner", tp)
	if v == nil {
		t.Fatal("an Apache file importing a GPL module must be a violation")
	}
	if !strings.Contains(v.message, "GPL-3.0-only") {
		t.Errorf("violation must name the offending license, got: %s", v.message)
	}
}

// @decision: licensing-enforced-by-license-lint
func TestPermittedThirdPartyModulesPass(t *testing.T) {
	tp := makeThirdPartyFixture(t)
	for _, imp := range []string{
		"github.com/google/uuid",
		"google.golang.org/grpc/codes",
		"gopkg.in/yaml.v3",
	} {
		if v := verifyThirdPartyImport("lib/protocols/x.go", imp, tp); v != nil {
			t.Errorf("import %q should pass, got violation: %s", imp, v.message)
		}
	}
}

// @decision: licensing-enforced-by-license-lint
func TestCompoundLicenseExpressionRequiresEveryTermPermitted(t *testing.T) {
	tp := makeThirdPartyFixture(t)
	if !tp.permits("MIT AND Apache-2.0") {
		t.Error("a compound expression whose every term is permitted must pass")
	}
	if tp.permits("MIT OR GPL-3.0-only") {
		t.Error("a compound expression naming an impermissible term must fail; the lint cannot pick the permissive branch on the project's behalf")
	}
}

// @decision: licensing-enforced-by-license-lint
func TestNoPermittedLicensesPermitsNothing(t *testing.T) {
	tp, err := newThirdPartyLicenses(nil, map[string]string{"example.com/x": "MIT"})
	if err != nil {
		t.Fatalf("newThirdPartyLicenses: %v", err)
	}
	if v := verifyThirdPartyImport("lib/protocols/x.go", "example.com/x", tp); v == nil {
		t.Fatal("with no permitted licenses configured, no third-party import may pass; the check fails closed rather than open")
	}
}

func TestModuleWithNoLicenseIsRejectedAtLoad(t *testing.T) {
	if _, err := newThirdPartyLicenses([]string{"MIT"}, map[string]string{"example.com/x": "  "}); err == nil {
		t.Fatal("a module listed with a blank license must be a configuration error")
	}
}

func TestStdlibImportsAreNotThirdParty(t *testing.T) {
	for _, imp := range []string{"fmt", "net/http", "encoding/json", "crypto/tls"} {
		if !isStdlibImport(imp) {
			t.Errorf("%q should be recognized as stdlib", imp)
		}
	}
	for _, imp := range []string{"github.com/google/uuid", "gopkg.in/yaml.v3", "golang.org/x/net/http2"} {
		if isStdlibImport(imp) {
			t.Errorf("%q should not be recognized as stdlib", imp)
		}
	}
}

// @decision: licensing-enforced-by-license-lint
func TestCommittedAllowlistCoversTheApacheSurfaceClosure(t *testing.T) {
	cfg, err := loadLicensingYAML("../..")
	if err != nil {
		t.Fatalf("loadLicensingYAML: %v", err)
	}
	if got := verifyApacheClosure(cfg, "../.."); len(got) != 0 {
		for _, v := range got {
			t.Errorf("%s: %s", v.path, v.message)
		}
	}
}
