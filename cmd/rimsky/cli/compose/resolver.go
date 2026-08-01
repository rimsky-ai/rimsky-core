// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
)

func ResolveTemplate(path string) (hash string, spec node.TemplateSpec, err error) {
	if _, err := os.Stat(path); err != nil {
		return "", node.TemplateSpec{}, err
	}
	if err := configload.LoadFile(path, &spec); err != nil {
		return "", node.TemplateSpec{}, err
	}
	node.ApplyFrameResolutionDefaults(&spec)
	hash, err = canonical.CanonicalSpecHash(spec)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	return hash, spec, nil
}
