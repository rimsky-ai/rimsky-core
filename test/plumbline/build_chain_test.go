// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	want := []string{"lint", "core-images", "service-images", "test-all", "scan", "push-images", "verify-published-platforms"}
	if strings.Join(deps, " ") != strings.Join(want, " ") {
		t.Errorf("release chain is %v, want the decided order %v", deps, want)
	}
}

// @decision: release-distribution
// @decision: release-chain
func TestEveryPushedTagIsAMultiPlatformIndexAndTheChainReadsItBack(t *testing.T) {
	makefile := readBuildFile(t, "Makefile")
	if !regexp.MustCompile(`(?m)^PUBLISH_PLATFORMS := linux/amd64,linux/arm64$`).MatchString(makefile) {
		t.Errorf("the published platform matrix is no longer exactly linux/amd64 + linux/arm64 — an operator on one processor would have no published image")
	}
	if !regexp.MustCompile(`(?s)BUILDX_PUSH = [^\n]*(\\\n[^\n]*)*--platform \$\(PUBLISH_PLATFORMS\)`).MatchString(makefile) {
		t.Errorf("push-images no longer builds every published tag for the whole platform matrix")
	}
	assertMakeTarget(t, makefile, "verify-published-platforms")
	if !strings.Contains(makefile, "./tools/verify-published-platforms.sh") {
		t.Errorf("the verify-published-platforms target no longer runs the script whose behaviour release_platform_verify_test.go drives")
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
	}
}

var goCompilesABinary = regexp.MustCompile(`\bgo build\b|\bgo test\b[^\n]*\s-c\b`)

var dockerfileEnvDisablesCGO = regexp.MustCompile(`(?m)^\s*ENV\s+CGO_ENABLED=0\b`)

var lineContinuation = regexp.MustCompile(`\\\s*\n\s*`)

var dockerfileStageStart = regexp.MustCompile(`(?i)^FROM\s`)

var dockerfileStagePinsBuildPlatform = regexp.MustCompile(`(?i)^FROM\s+--platform=\$\{?BUILDPLATFORM\}?\s`)

var dockerfileDeclaresTargetOS = regexp.MustCompile(`(?m)^\s*ARG\s+TARGETOS\b`)

var dockerfileDeclaresTargetArch = regexp.MustCompile(`(?m)^\s*ARG\s+TARGETARCH\b`)

type dockerfileCompile struct {
	text                    string
	stageDisablesCGO        bool
	stagePinsBuildPlatform  bool
	stageDeclaresTargetOS   bool
	stageDeclaresTargetArch bool
}

func dockerfileGoCompileLines(src string) []dockerfileCompile {
	var out []dockerfileCompile
	var stage dockerfileCompile
	for _, line := range strings.Split(lineContinuation.ReplaceAllString(src, " "), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if dockerfileStageStart.MatchString(trimmed) {
			stage = dockerfileCompile{stagePinsBuildPlatform: dockerfileStagePinsBuildPlatform.MatchString(trimmed)}
			continue
		}
		if dockerfileEnvDisablesCGO.MatchString(trimmed) {
			stage.stageDisablesCGO = true
		}
		if dockerfileDeclaresTargetOS.MatchString(trimmed) {
			stage.stageDeclaresTargetOS = true
		}
		if dockerfileDeclaresTargetArch.MatchString(trimmed) {
			stage.stageDeclaresTargetArch = true
		}
		for _, invocation := range strings.Split(trimmed, "&&") {
			invocation = strings.TrimSpace(invocation)
			if !goCompilesABinary.MatchString(invocation) {
				continue
			}
			compile := stage
			compile.text = invocation
			out = append(out, compile)
		}
	}
	return out
}

var compileTakesItsTargetFromTheBuild = regexp.MustCompile(`GOOS=\$\{?TARGETOS\}?\s+GOARCH=\$\{?TARGETARCH\}?`)

// @decision: release-distribution
func TestEveryGoCompilingImageCrossCompilesForTheRequestedPlatform(t *testing.T) {
	repoRoot := findRepoRoot(t)
	compiling := 0
	for _, rel := range dockerfilesUnder(t, repoRoot) {
		lines := dockerfileGoCompileLines(readBuildFile(t, rel))
		if len(lines) == 0 {
			continue
		}
		compiling++
		for _, line := range lines {
			if !line.stagePinsBuildPlatform {
				t.Errorf("%s compiles Go in a stage whose FROM carries no `--platform=$BUILDPLATFORM`, so a multi-platform build runs the toolchain under emulation instead of cross-compiling: %s", rel, line.text)
			}
			if !line.stageDeclaresTargetOS || !line.stageDeclaresTargetArch {
				t.Errorf("%s compiles Go in a stage declaring no `ARG TARGETOS` / `ARG TARGETARCH`, so the build cannot read the platform buildx asked for: %s", rel, line.text)
			}
			if !compileTakesItsTargetFromTheBuild.MatchString(line.text) {
				t.Errorf("%s pins the compile to a fixed platform instead of `GOOS=$TARGETOS GOARCH=$TARGETARCH`, so every published platform would carry the same binary: %s", rel, line.text)
			}
		}
	}
	if compiling != dockerfilesCompilingGo {
		t.Errorf("%d Dockerfiles compile Go, want the %d this check enumerates", compiling, dockerfilesCompilingGo)
	}
}

const dockerfilesCompilingGo = 18

// @decision: build-cgo-disabled
func TestAStageLevelCGOEnvNeverExemptsAnotherStagesCompile(t *testing.T) {
	body := "FROM golang AS builder\n" +
		"RUN go build ./cmd/one\n" +
		"FROM golang AS second\n" +
		"ENV CGO_ENABLED=0\n" +
		"RUN go build ./cmd/two\n" +
		"RUN CGO_ENABLED=0 GOOS=linux \\\n    go build ./cmd/three\n" +
		"RUN CGO_ENABLED=0 go build ./cmd/four && \\\n    go build ./cmd/five\n"
	got := dockerfileGoCompileLines(body)
	if len(got) != 5 {
		t.Fatalf("compile lines = %+v, want the five go build invocations", got)
	}
	if got[0].stageDisablesCGO {
		t.Errorf("the builder stage carries no ENV CGO_ENABLED=0, yet a later stage's ENV exempted it: %+v", got[0])
	}
	if !got[1].stageDisablesCGO {
		t.Errorf("a compile in the stage that sets ENV CGO_ENABLED=0 is not covered by it: %+v", got[1])
	}
	if !strings.Contains(got[2].text, "CGO_ENABLED=0 GOOS=linux  go build") {
		t.Errorf("a backslash continuation no longer reads as one invocation: %q", got[2].text)
	}
	if !strings.Contains(got[3].text, "CGO_ENABLED=0 go build ./cmd/four") {
		t.Errorf("the first link of an && chain is not read as its own invocation: %q", got[3].text)
	}
	if strings.Contains(got[4].text, "CGO_ENABLED=0") {
		t.Errorf("a later link of an && chain inherits the first link's env, so a compile added without it could never fail the check: %q", got[4].text)
	}
}

// @decision: build-cgo-disabled
func TestEveryBuildInvocationInTheTreeDisablesCGO(t *testing.T) {
	repoRoot := findRepoRoot(t)

	var compiling []string
	for _, rel := range dockerfilesUnder(t, repoRoot) {
		body := readBuildFile(t, rel)
		lines := dockerfileGoCompileLines(body)
		if len(lines) == 0 {
			continue
		}
		compiling = append(compiling, rel)
		for _, line := range lines {
			if line.stageDisablesCGO || strings.Contains(line.text, "CGO_ENABLED=0") {
				continue
			}
			t.Errorf("%s compiles Go without CGO_ENABLED=0, and its own build stage sets no `ENV CGO_ENABLED=0` either: %s", rel, line.text)
		}
	}
	if len(compiling) != dockerfilesCompilingGo {
		t.Errorf("%d Dockerfiles compile Go, want the %d this check enumerates — a new one must be held to the CGO posture too:\n  %s",
			len(compiling), dockerfilesCompilingGo, strings.Join(compiling, "\n  "))
	}

	for _, line := range goCompileLines(readBuildFile(t, "Makefile")) {
		if !strings.Contains(line, "CGO_ENABLED=0") {
			t.Errorf("a Makefile target compiles Go without CGO_ENABLED=0: %s", line)
		}
	}

	goreleaser := readBuildFile(t, ".goreleaser.yaml")
	if !regexp.MustCompile(`(?s)builds:.*?env:\s*\n\s*-\s*CGO_ENABLED=0`).MatchString(goreleaser) {
		t.Errorf(".goreleaser.yaml builds the CLI archives without CGO_ENABLED=0")
	}
}

func dockerfilesUnder(t *testing.T, repoRoot string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".ok-planner", "node_modules", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), "Dockerfile") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		found = append(found, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s for Dockerfiles: %v", repoRoot, err)
	}
	sort.Strings(found)
	return found
}

func goCompileLines(src string) []string {
	var out []string
	for _, line := range strings.Split(lineContinuation.ReplaceAllString(src, " "), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, invocation := range strings.Split(trimmed, "&&") {
			invocation = strings.TrimSpace(invocation)
			if goCompilesABinary.MatchString(invocation) {
				out = append(out, invocation)
			}
		}
	}
	return out
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

func TestNoRaceDetectorInAnyBuildGate(t *testing.T) {
	uncommentedRaceFlag := regexp.MustCompile(`(?m)^[^#\n]*\B-race\b`)
	for _, rel := range []string{"Makefile", ".github/workflows/ci.yml"} {
		if uncommentedRaceFlag.MatchString(readBuildFile(t, rel)) {
			t.Errorf("%s wires the race detector into a build gate — the suite's verdict must be a function of the code alone, "+
				"and a green race-detector run proves nothing, so gating on one makes the gate's verdict probabilistic", rel)
		}
	}
}
