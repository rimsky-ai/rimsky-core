// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package imagetag

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const EnvVar = "RIMSKY_IMAGE_TAG"

const BuildCommand = "make core-images service-images test-images"

const Script = "tools/image-src-tag.sh"

var (
	srcTagOnce  sync.Once
	srcTagValue string
	srcTagErr   error
)

func Ref(repo string) string {
	if tag := os.Getenv(EnvVar); tag != "" {
		return repo + ":" + tag
	}
	srcTagOnce.Do(func() {
		srcTagValue, srcTagErr = deriveSrcTag()
	})
	if srcTagErr != nil {
		panic(fmt.Sprintf("imagetag: cannot derive the source-tree image tag for %q (set %s to override): %v",
			repo, EnvVar, srcTagErr))
	}
	return repo + ":" + srcTagValue
}

func deriveSrcTag() (string, error) {
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", commandError(err))
	}
	root := strings.TrimSpace(string(rootOut))
	cmd := exec.Command(filepath.Join(root, Script))
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", Script, commandError(err))
	}
	tag := strings.TrimSpace(string(out))
	if !strings.HasPrefix(tag, "src-") {
		return "", fmt.Errorf("%s printed %q, want a src-<tree-hash> tag", Script, tag)
	}
	return tag, nil
}

func commandError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
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
	return fmt.Errorf("image %q is not in the local docker daemon — rimsky image tags resolve from the current "+
		"source tree (or %s when set) and never fall back to :latest; build this tree's images with `%s`: %w",
		img, EnvVar, BuildCommand, err)
}
