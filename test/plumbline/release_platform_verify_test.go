// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const stubDockerScript = `#!/bin/sh
set -eu
for arg in "$@"; do ref="$arg"; done
img=${ref##*/}
img=${img%%:*}
payload="$PAYLOAD_DIR/$img.json"
if [ ! -f "$payload" ]; then
  echo "ERROR: $ref: not found" >&2
  exit 1
fi
cat "$payload"
`

const twoPlatformIndexWithAttestations = `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.index.v1+json",
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:aaa",
      "size": 668,
      "platform": { "architecture": "amd64", "os": "linux" }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:bbb",
      "size": 668,
      "platform": { "os": "linux", "architecture": "arm64" }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:ccc",
      "size": 1108,
      "annotations": { "vnd.docker.reference.type": "attestation-manifest" },
      "platform": { "architecture": "unknown", "os": "unknown" }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:ddd",
      "size": 1108,
      "annotations": { "vnd.docker.reference.type": "attestation-manifest" },
      "platform": { "architecture": "unknown", "os": "unknown" }
    }
  ]
}`

const singlePlatformIndex = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[` +
	`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:aaa","size":668,"platform":{"architecture":"arm64","os":"linux"}},` +
	`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:ccc","size":1108,"platform":{"architecture":"unknown","os":"unknown"}}]}`

const bareManifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
	`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:cfg","size":1234},` +
	`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:lyr","size":4567}]}`

func runPlatformVerify(t *testing.T, images string, payloads map[string]string) (string, error) {
	t.Helper()
	repoRoot := findRepoRoot(t)
	dir := t.TempDir()

	payloadDir := filepath.Join(dir, "payloads")
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		t.Fatalf("mkdir payloads: %v", err)
	}
	for image, body := range payloads {
		if err := os.WriteFile(filepath.Join(payloadDir, image+".json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write payload %s: %v", image, err)
		}
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(stubDockerScript), 0o755); err != nil {
		t.Fatalf("write stub docker: %v", err)
	}

	cmd := exec.Command(filepath.Join(repoRoot, "tools", "verify-published-platforms.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PAYLOAD_DIR="+payloadDir,
		"REGISTRY=registry.example/rimskyai",
		"VERSION=v1.2.3",
		"LATEST_TAG=latest",
		"PLATFORMS=linux/amd64,linux/arm64",
		"IMAGES="+images,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// @decision: release-chain
// @decision: release-distribution
func TestPlatformVerifyPassesATagPublishedForEveryPlatform(t *testing.T) {
	out, err := runPlatformVerify(t, "rimsky", map[string]string{"rimsky": twoPlatformIndexWithAttestations})
	if err != nil {
		t.Fatalf("verify rejected a two-platform index: %v\n%s", err, out)
	}
	if !strings.Contains(out, "every pushed tag publishes linux/amd64,linux/arm64") {
		t.Errorf("the passing report does not name the platform matrix: %s", out)
	}
	if strings.Contains(out, "unknown") {
		t.Errorf("the attestation manifests are reported as platforms: %s", out)
	}
}

// @decision: release-chain
func TestPlatformVerifyFailsATagPublishedForOnePlatform(t *testing.T) {
	out, err := runPlatformVerify(t, "rimsky", map[string]string{"rimsky": singlePlatformIndex})
	if err == nil {
		t.Fatalf("verify accepted a single-platform index — an operator on Intel would have no image:\n%s", out)
	}
	if !strings.Contains(out, "does not publish linux/amd64") {
		t.Errorf("the failure does not name the missing platform: %s", out)
	}
	if !strings.Contains(out, "registry.example/rimskyai/rimsky:v1.2.3") {
		t.Errorf("the failure does not name the tag it read: %s", out)
	}
	if !strings.Contains(out, "it carries: linux/arm64") {
		t.Errorf("the failure does not name the platform the tag does carry: %s", out)
	}
}

// @decision: release-chain
func TestPlatformVerifyFailsATagThatIsASingleManifestRatherThanAnIndex(t *testing.T) {
	out, err := runPlatformVerify(t, "rimsky", map[string]string{"rimsky": bareManifest})
	if err == nil {
		t.Fatalf("verify accepted a bare manifest, which names no platform at all:\n%s", out)
	}
	for _, want := range []string{"does not publish linux/amd64", "does not publish linux/arm64"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure does not report %q: %s", want, out)
		}
	}
}

// @decision: release-chain
func TestPlatformVerifyFailsATagTheRegistryDoesNotHold(t *testing.T) {
	out, err := runPlatformVerify(t, "rimsky", map[string]string{})
	if err == nil {
		t.Fatalf("verify accepted a tag the registry could not return:\n%s", out)
	}
	if !strings.Contains(out, "cannot read registry.example/rimskyai/rimsky:v1.2.3") {
		t.Errorf("the failure does not name the tag it could not read: %s", out)
	}
}

// @decision: release-chain
func TestPlatformVerifyChecksEveryPublishedImageAndBothItsTags(t *testing.T) {
	out, err := runPlatformVerify(t, "rimsky rimsky-all-in-one", map[string]string{
		"rimsky":            twoPlatformIndexWithAttestations,
		"rimsky-all-in-one": singlePlatformIndex,
	})
	if err == nil {
		t.Fatalf("one good image hid a bad one:\n%s", out)
	}
	if strings.Count(out, "rimsky-all-in-one") < 2 {
		t.Errorf("the failing image's :v1.2.3 and :latest tags are not both reported: %s", out)
	}
	if !strings.Contains(out, "2 published tag/platform check(s) failed") {
		t.Errorf("the summary does not count both failing tags: %s", out)
	}
}
