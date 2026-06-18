// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
)

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
