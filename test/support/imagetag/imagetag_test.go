// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package imagetag

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageRefResolvesByThePerRunTagInTheEnvironment(t *testing.T) {
	t.Setenv(EnvVar, "run-0123456789ab")
	got := Ref("rimsky-all-in-one")
	want := "rimsky-all-in-one:run-0123456789ab"
	if got != want {
		t.Fatalf("Ref with %s set: got %q, want %q", EnvVar, got, want)
	}
}

func TestAnUnsetTagResolvesToANameNoRegistryHoldsAndFailsNamingTheVariable(t *testing.T) {
	t.Setenv(EnvVar, "")
	ref := Ref("rimsky-all-in-one")
	if ref != "rimsky-all-in-one:"+UnsetTag {
		t.Fatalf("Ref with %s unset = %q, want the self-describing %q tag", EnvVar, ref, UnsetTag)
	}
	if !strings.Contains(UnsetTag, EnvVar) {
		t.Errorf("the unset tag %q does not name the variable, so a raw docker error would not say what is missing", UnsetTag)
	}

	cause := errors.New("pull access denied for rimsky-all-in-one, repository does not exist")
	if !IsMissingLocalImage(ref, cause) {
		t.Fatalf("a run with no tag is not reported as a missing rimsky image, so the caller cannot explain it")
	}
	msg := MissingImageError(ref, cause).Error()
	for _, want := range []string{EnvVar, BuildCommand, "rimsky-all-in-one"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the unset-tag failure does not name %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, ":latest") && !strings.Contains(msg, "never :latest") {
		t.Errorf("the unset-tag failure offers :latest as a way out: %s", msg)
	}
}

func TestMissingImageErrorNamesTheVariableAndTheBuildCommand(t *testing.T) {
	cause := errors.New("No such image: rimsky-all-in-one:run-0123456789ab")
	err := MissingImageError("rimsky-all-in-one:run-0123456789ab", cause)
	for _, want := range []string{EnvVar, BuildCommand, "rimsky-all-in-one:run-0123456789ab"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the missing-image error does not name %q: %v", want, err)
		}
	}
	if !errors.Is(err, cause) {
		t.Errorf("the missing-image error drops the docker failure it wraps: %v", err)
	}
}

func TestMissingImageErrorFiresOnlyForRimskyImages(t *testing.T) {
	cause := errors.New("No such image: postgres:16")
	if IsMissingLocalImage("postgres:16", cause) {
		t.Errorf("a missing third-party image is reported as a rimsky build gap")
	}
	if !IsMissingLocalImage("rimsky-all-in-one:run-0123456789ab", cause) {
		t.Errorf("a missing rimsky image is not reported as a rimsky build gap")
	}
}

func TestRunTagScriptMintsAFreshTagOnEveryCall(t *testing.T) {
	script := filepath.Join(repoRoot(t), RunTagScript)
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the run-tag script the profile declares is missing: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		tag := mint(t, script)
		if !strings.HasPrefix(tag, "run-") || len(tag) != len("run-")+12 {
			t.Fatalf("%s printed %q, want a run-<12 hex> tag", RunTagScript, tag)
		}
		if seen[tag] {
			t.Fatalf("%s printed %q twice — a per-run tag must be fresh on every call, or two runs share images", RunTagScript, tag)
		}
		seen[tag] = true
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func mint(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command(script).Output()
	if err != nil {
		t.Fatalf("%s: %v", script, err)
	}
	return strings.TrimSpace(string(out))
}
