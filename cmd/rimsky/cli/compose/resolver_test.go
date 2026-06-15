// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
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
