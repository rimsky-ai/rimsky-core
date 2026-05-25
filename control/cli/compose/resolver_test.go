// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package compose

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/template/canonical"
)

const exampleSpec = `name: example
version: "1.0"
frame_resolution_mode: coalesce
nodes:
  - type: hello
    executor: http-node
`

func TestResolveTemplate_HashMatchesCanonical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yml")
	if err := os.WriteFile(path, []byte(exampleSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	gotHash, gotSpec, err := ResolveTemplate(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash == "" {
		t.Error("hash empty")
	}
	if len(gotSpec.Nodes) == 0 {
		t.Errorf("spec missing nodes: %+v", gotSpec)
	}
	// Cross-check against direct canonical hash.
	var domainSpec node.TemplateSpec
	if err := yaml.Unmarshal([]byte(exampleSpec), &domainSpec); err != nil {
		t.Fatal(err)
	}
	node.ApplyFrameResolutionDefaults(&domainSpec)
	wantHash, err := canonical.CanonicalSpecHash(domainSpec)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != wantHash {
		t.Errorf("hash drift: got %q want %q", gotHash, wantHash)
	}
}
