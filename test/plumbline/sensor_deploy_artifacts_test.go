// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledSensorsShipDockerfilesAndMakefileImageEntries(t *testing.T) {
	repoRoot := findRepoRoot(t)
	makefileRaw, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(makefileRaw)

	sensors := []struct {
		name          string
		dockerfileRel string
		image         string
	}{
		{"sensor-cron", "lib/services/sensors/sensor-cron/Dockerfile.sensor-cron", "rimsky-sensor-cron"},
		{"sensor-http", "lib/services/sensors/sensor-http/Dockerfile.sensor-http", "rimsky-sensor-http"},
		{"sensor-object-store", "lib/services/sensors/sensor-object-store/Dockerfile.sensor-object-store", "rimsky-sensor-object-store"},
		{"sensor-webhook", "lib/services/sensors/sensor-webhook/Dockerfile.sensor-webhook", "rimsky-sensor-webhook"},
	}

	imagesBlockStart := strings.Index(makefile, "IMAGES := ")
	if imagesBlockStart < 0 {
		t.Fatal("Makefile has no IMAGES := published-image list")
	}
	imagesBlockEnd := strings.Index(makefile[imagesBlockStart:], "\n\n")
	if imagesBlockEnd < 0 {
		t.Fatal("could not find end of IMAGES block")
	}
	imagesBlock := makefile[imagesBlockStart : imagesBlockStart+imagesBlockEnd]

	serviceImagesStart := strings.Index(makefile, "\nservice-images:")
	pushImagesStart := strings.Index(makefile, "\npush-images:")
	if serviceImagesStart < 0 {
		t.Fatal("Makefile has no service-images target")
	}
	if pushImagesStart < 0 {
		t.Fatal("Makefile has no push-images target")
	}
	serviceImagesBlock := makefile[serviceImagesStart:pushImagesStart]
	pushImagesBlock := makefile[pushImagesStart:]

	for _, sensor := range sensors {
		dockerfilePath := filepath.Join(repoRoot, filepath.FromSlash(sensor.dockerfileRel))
		if _, statErr := os.Stat(dockerfilePath); statErr != nil {
			t.Errorf("%s: Dockerfile missing at %s: %v", sensor.name, sensor.dockerfileRel, statErr)
		}
		if !strings.Contains(serviceImagesBlock, sensor.dockerfileRel) {
			t.Errorf("%s: `service-images` target has no docker build line referencing %s", sensor.name, sensor.dockerfileRel)
		}
		if !strings.Contains(serviceImagesBlock, sensor.image+":$(VERSION)") &&
			!strings.Contains(serviceImagesBlock, ","+sensor.image+")") {
			t.Errorf("%s: `service-images` target neither tags %s:$(VERSION) directly nor builds it via $(call build-image,...,%s)",
				sensor.name, sensor.image, sensor.image)
		}
		if !strings.Contains(pushImagesBlock, sensor.dockerfileRel) {
			t.Errorf("%s: `push-images` target has no build line referencing %s", sensor.name, sensor.dockerfileRel)
		}
		if !strings.Contains(pushImagesBlock, "/"+sensor.image+":$(VERSION)") {
			t.Errorf("%s: `push-images` target has no $(REGISTRY)/%s:$(VERSION) tag", sensor.name, sensor.image)
		}
		if !strings.Contains(imagesBlock, sensor.image) {
			t.Errorf("%s: published IMAGES list is missing %s — a dropped entry here silently excludes the sensor from `make scan` and the release image count", sensor.name, sensor.image)
		}
	}
}
