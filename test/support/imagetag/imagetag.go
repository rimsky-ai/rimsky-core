// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package imagetag

import (
	"fmt"
	"os"
	"strings"
)

const EnvVar = "RIMSKY_IMAGE_TAG"

const RunTagScript = ".ok-workspaces/bin/run-tag"

const BuildCommand = "RIMSKY_IMAGE_TAG=$(" + RunTagScript + ") make core-images service-images test-images"

const UnsetTag = "unset-" + EnvVar

func Ref(repo string) string {
	tag := os.Getenv(EnvVar)
	if tag == "" {
		return repo + ":" + UnsetTag
	}
	return repo + ":" + tag
}

func IsMissingLocalImage(img string, err error) bool {
	if !strings.HasPrefix(img, "rimsky") {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"No such image",
		"not found",
		"pull access denied",
		"failed to resolve reference",
		"repository does not exist",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func MissingImageError(img string, err error) error {
	if strings.HasSuffix(img, ":"+UnsetTag) {
		return fmt.Errorf("%s is unset, so no image tag resolves for %q — every rimsky image a suite consumes is "+
			"built and verified under one per-run tag, with no fallback and never :latest; mint a tag and build "+
			"this tree's images under it with `%s`, then export the same tag for the test run (`make test-all` "+
			"and `make test-in-stack` do both for you): %w",
			EnvVar, strings.TrimSuffix(img, ":"+UnsetTag), BuildCommand, err)
	}
	return fmt.Errorf("image %q is not in the local docker daemon — rimsky images resolve by %s alone, with no "+
		"fallback and never :latest, so this run's tag names no such image; build this tree's images under a "+
		"freshly minted tag with `%s` and export the same tag for the test run: %w",
		img, EnvVar, BuildCommand, err)
}
