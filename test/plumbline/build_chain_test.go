// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readBuildFile(t *testing.T, rel string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(findRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(src)
}

func assertMakeTarget(t *testing.T, makefile, target string) {
	t.Helper()
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).MatchString(makefile) {
		t.Errorf("Makefile no longer declares the %q target", target)
	}
}

// @decision: build-tool-makefile
// @decision: licensing-enforced-by-license-lint
func TestMakefileIsTheBuildOrchestrationSourceOfTruth(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	for _, target := range []string{"build-all", "test-all", "lint", "tidy", "proto-gen", "core-images", "service-images", "test-images", "license-lint", "release"} {
		assertMakeTarget(t, makefile, target)
	}
	if !regexp.MustCompile(`(?m)^lint:.*\blicense-lint\b`).MatchString(makefile) {
		t.Errorf("plain `make lint` no longer runs license-lint — the licensing boundary check would only run at release")
	}
}

// @decision: release-chain
func TestReleaseChainOrder(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	m := regexp.MustCompile(`(?m)^release:(.*)$`).FindStringSubmatch(makefile)
	if m == nil {
		t.Fatalf("Makefile has no release target")
	}
	deps := strings.Fields(m[1])
	want := []string{"lint", "core-images", "service-images", "test-all", "test-race", "scan", "push-images"}
	if strings.Join(deps, " ") != strings.Join(want, " ") {
		t.Errorf("release chain is %v, want the decided order %v", deps, want)
	}
}

// @decision: image-set-four-core
func TestCoreImageSet(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	repoRoot := findRepoRoot(t)
	coreImages := map[string]string{
		"rimsky":                  "dockerfiles/Dockerfile.rimsky",
		"rimsky-all-in-one":       "dockerfiles/Dockerfile.all-in-one",
		"rimsky-host-agent-proxy": "dockerfiles/Dockerfile.go-base",
		"rimsky-conformance":      "dockerfiles/Dockerfile.conformance",
	}
	for image, dockerfile := range coreImages {
		if !strings.Contains(makefile, dockerfile+","+image) {
			t.Errorf("core-images no longer builds %s from %s", image, dockerfile)
		}
		if _, err := os.Stat(filepath.Join(repoRoot, dockerfile)); err != nil {
			t.Errorf("core image definition %s is missing: %v", dockerfile, err)
		}
	}
}

// @decision: image-set-bundled-services
func TestBundledServiceImagesBuildFromColocatedDockerfiles(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	assertMakeTarget(t, makefile, "service-images")
	refs := regexp.MustCompile(`\$\(call build-image,(lib/services/[^,]+),`).FindAllStringSubmatch(makefile, -1)
	if len(refs) == 0 {
		t.Fatalf("service-images builds no images from lib/services/ — the one-image-per-bundled-service set is gone")
	}
	repoRoot := findRepoRoot(t)
	for _, m := range refs {
		if _, err := os.Stat(filepath.Join(repoRoot, m[1])); err != nil {
			t.Errorf("service image definition %s is referenced by the Makefile but missing: %v", m[1], err)
		}
	}
}

// @decision: image-two-stage
// @decision: build-cgo-disabled
func TestImagesAreTwoStageStaticNonRoot(t *testing.T) {
	for _, rel := range []string{"dockerfiles/Dockerfile.rimsky", "dockerfiles/Dockerfile.go-base", "dockerfiles/Dockerfile.conformance"} {
		df := readBuildFile(t, rel)
		fromCount := len(regexp.MustCompile(`(?m)^FROM `).FindAllString(df, -1))
		if fromCount < 2 {
			t.Errorf("%s is no longer a two-stage build (%d FROM lines)", rel, fromCount)
		}
		if !strings.Contains(df, "distroless") {
			t.Errorf("%s runtime stage is no longer distroless", rel)
		}
		if !strings.Contains(df, "nonroot") {
			t.Errorf("%s no longer runs as a non-root user", rel)
		}
		if !strings.Contains(df, "CGO_ENABLED=0") {
			t.Errorf("%s builds without CGO_ENABLED=0 — CGO is disabled for all builds", rel)
		}
	}
}

// @decision: image-tagging-version-and-channel
func TestImageTaggingVersionAndChannel(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	if !strings.Contains(makefile, ":$(VERSION)") {
		t.Errorf("images are no longer tagged with the immutable per-version tag")
	}
	if !strings.Contains(makefile, ":latest") {
		t.Errorf("images no longer carry the release channel tag")
	}
	if !regexp.MustCompile(`LATEST_TAG=dev|--tag dev`).MatchString(makefile) {
		t.Errorf("the dev release channel tag is gone — dev releases would land on the release channel")
	}
}

// @decision: registry-hub-rimskyai-namespace
func TestRegistryNamespaceIsHyphenless(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	m := regexp.MustCompile(`(?m)^REGISTRY \?= (.+)$`).FindStringSubmatch(makefile)
	if m == nil {
		t.Fatalf("Makefile no longer declares a default REGISTRY")
	}
	if !strings.Contains(m[1], "rimskyai") || strings.Contains(m[1], "rimsky-ai") {
		t.Errorf("default registry namespace is %q, want the hyphenless rimskyai namespace", m[1])
	}
}

// @decision: release-attestations
func TestReleasePushCarriesAttestations(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	if !strings.Contains(makefile, "--provenance=mode=max") {
		t.Errorf("push-images no longer requests max-mode provenance attestations")
	}
	if !strings.Contains(makefile, "--sbom=true") {
		t.Errorf("push-images no longer requests SBOM attestations")
	}
}

// @decision: release-scan-docker-scout
func TestReleaseScanGatesOnCriticalAndHigh(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	if !strings.Contains(makefile, "docker scout cves") {
		t.Errorf("the release chain no longer scans images with docker scout")
	}
	if !strings.Contains(makefile, "--only-severity critical,high") {
		t.Errorf("the scan no longer gates on critical/high findings")
	}
}

// @decision: release-dev-mechanical
// @decision: release-semver-sha-dot-joined
func TestDevReleaseVersionIsMechanicalNextMinorDotJoinedSha(t *testing.T) {
	script := readBuildFile(t, "tools/dev-release.sh")
	if !regexp.MustCompile(`NEXT_MINOR_BASE="v\$\{MAJOR\}\.\$\(\(MINOR \+ 1\)\)\.0"`).MatchString(script) {
		t.Errorf("dev-release.sh no longer derives the next-minor base version — the mechanical, no-SemVer-judgment version derivation is gone")
	}
	if !regexp.MustCompile(`DEV_VERSION="\$\{NEXT_MINOR_BASE\}-dev\.\$\{DATE\}\.g\$\{SHA\}"`).MatchString(script) {
		t.Errorf("dev-release.sh no longer dot-joins the date and commit SHA into the pre-release segment")
	}
	if strings.Contains(script, "+g${SHA}") || strings.Contains(script, "+${SHA}") {
		t.Errorf("dev-release.sh appends the SHA as SemVer build metadata (`+`) instead of dot-joining it into the pre-release segment")
	}
}

// @decision: release-distribution
func TestGoreleaserCLIArchiveChannelMatchesDecision(t *testing.T) {
	cfg := readBuildFile(t, ".goreleaser.yaml")
	if !regexp.MustCompile(`(?s)goos:\s*\n\s*-\s*linux\s*\n\s*-\s*darwin`).MatchString(cfg) {
		t.Errorf(".goreleaser.yaml no longer builds exactly linux+darwin — the decided CLI-archive platform set changed")
	}
	if regexp.MustCompile(`(?m)^\s*-\s*windows\s*$`).MatchString(cfg) {
		t.Errorf(".goreleaser.yaml now builds windows — the decision names Windows as a deliberate non-channel")
	}
	if !regexp.MustCompile(`(?s)goarch:\s*\n\s*-\s*amd64\s*\n\s*-\s*arm64`).MatchString(cfg) {
		t.Errorf(".goreleaser.yaml no longer builds exactly amd64+arm64 — the decided CLI-archive arch set changed")
	}
	if !regexp.MustCompile(`(?s)sboms:\s*\n\s*-.*\n\s*artifacts:\s*archive`).MatchString(cfg) {
		t.Errorf(".goreleaser.yaml no longer publishes a per-archive SBOM")
	}
}

// @decision: race-gate-split
func TestRaceGateSplit(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	assertMakeTarget(t, makefile, "test-race")
	if !regexp.MustCompile(`-race -count=1`).MatchString(makefile) {
		t.Errorf("the everyday gate no longer carries a single-iteration race slice")
	}
	if !regexp.MustCompile(`-race -count=3`).MatchString(makefile) {
		t.Errorf("the dedicated race gate no longer repeats under the race detector")
	}
}
