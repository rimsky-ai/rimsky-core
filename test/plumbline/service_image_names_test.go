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

var buildImageCall = regexp.MustCompile(`\$\(call build-image,([^,)]+),([^,)]+)`)

func imageNamesFromTarget(t *testing.T, root, target string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	var names []string
	inTarget := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, target+":") {
			inTarget = true
			continue
		}
		if inTarget && !strings.HasPrefix(line, "\t") {
			break
		}
		if !inTarget {
			continue
		}
		if m := buildImageCall.FindStringSubmatch(line); m != nil {
			names = append(names, strings.TrimSpace(m[2]))
		}
	}
	if len(names) == 0 {
		t.Fatalf("Makefile target %q builds no images", target)
	}
	sort.Strings(names)
	return names
}

var serviceKinds = []string{"claim-producer", "executor", "sensor", "subscriber"}

// @concept: service
func TestEveryShippedServiceImageNameIsDerivableFromItsKindAndName(t *testing.T) {
	root := findRepoRoot(t)

	scheme := regexp.MustCompile(`^rimsky-(` + strings.Join(serviceKinds, "|") + `)-(.+)$`)

	serviceImages := imageNamesFromTarget(t, root, "service-images")
	if len(serviceImages) != 11 {
		t.Fatalf("service images = %d, want 11: %v", len(serviceImages), serviceImages)
	}

	kindsSeen := map[string]int{}
	for _, name := range serviceImages {
		m := scheme.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("service image %q does not follow rimsky-<kind>-<name>, so a reader cannot derive it from the kind and the name", name)
			continue
		}
		kindsSeen[m[1]]++
	}
	for _, kind := range serviceKinds {
		if kindsSeen[kind] == 0 {
			t.Errorf("no shipped service image names the kind %q, so the scheme is unproven for it", kind)
		}
	}

	coreImages := imageNamesFromTarget(t, root, "core-images")
	want := []string{"rimsky", "rimsky-all-in-one", "rimsky-conformance", "rimsky-host-agent-proxy"}
	if strings.Join(coreImages, ",") != strings.Join(want, ",") {
		t.Fatalf("core images = %v, want %v", coreImages, want)
	}
	for _, name := range coreImages {
		if scheme.MatchString(name) {
			t.Errorf("core image %q names a service kind; the four core images are not services of a kind", name)
		}
	}
}
