package compose

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/core/canonical"
	"github.com/fallguy/rimsky/core/node"
)

const exampleSpec = `name: example
version: "1.0"
frame_resolution: coalesce
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
	if _, ok := gotSpec["nodes"]; !ok {
		t.Errorf("specMap missing nodes: %+v", gotSpec)
	}
	// Cross-check against direct canonical hash.
	var spec node.TemplateSpec
	_ = spec
	// Build the same spec the way ResolveTemplate does and compare.
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
