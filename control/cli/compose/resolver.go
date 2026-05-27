// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// resolver.go — read a template spec file from disk, apply
// frame-resolution defaults, and compute its content hash via the
// shared canonical hasher (matching the control-api's hash exactly).
package compose

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/template/canonical"
)

// ResolveTemplate reads a template spec file from disk, runs
// frame-resolution default-fill on the typed view, and returns:
//
//   - the canonical hash (computed from the typed TemplateSpec, matching
//     the control-api exactly), and
//   - the typed TemplateSpec to ship verbatim to POST /templates.
//
// After the 2026-05-02 json-tags cleanup the typed view marshals to
// the same lowercase-snake-case JSON keys the control-api decodes
// (`name`, `version`, `frame_resolution`, `nodes`, …), so no wire
// shaping or YAML→generic-map round-trip is needed.
func ResolveTemplate(path string) (hash string, spec node.TemplateSpec, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return "", node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	node.ApplyFrameResolutionDefaults(&spec)
	hash, err = canonical.CanonicalSpecHash(spec)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	return hash, spec, nil
}
