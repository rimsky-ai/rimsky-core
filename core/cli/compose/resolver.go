// resolver.go — read a template spec file from disk, apply
// frame-resolution defaults, and compute its content hash via the
// shared canonical hasher (matching the control-api's hash exactly).
package compose

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/core/canonical"
	"github.com/fallguy/rimsky/core/node"
)

// ResolveTemplate reads a template spec file from disk, runs
// frame-resolution default-fill on the typed view, and returns:
//
//   - the canonical hash (computed from the typed TemplateSpec, matching
//     the control-api exactly), and
//   - the JSON-shaped spec map that should be sent verbatim to POST
//     /templates. The map is the YAML-parsed-as-generic form so its
//     keys match the control-api's lowercase JSON contract (`name`,
//     `version`, `frame_resolution`, `nodes`, …).
func ResolveTemplate(path string) (hash string, specMap map[string]any, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var spec node.TemplateSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", path, err)
	}
	node.ApplyFrameResolutionDefaults(&spec)
	hash, err = canonical.CanonicalSpecHash(spec)
	if err != nil {
		return "", nil, err
	}

	// Build the wire-shape spec from the original YAML to preserve the
	// lowercase keys the control-api decodes via templateDeployRequest.
	var rawSpec any
	if err := yaml.Unmarshal(raw, &rawSpec); err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", path, err)
	}
	jsonShaped, err := yamlToJSON(rawSpec)
	if err != nil {
		return "", nil, fmt.Errorf("convert %s: %w", path, err)
	}
	m, ok := jsonShaped.(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("%s: spec must be a YAML object", path)
	}
	// Default frame_timeout_ms is applied on the typed side; reflect
	// that on the wire too so the registered row matches the hash.
	if _, ok := m["frame_timeout_ms"]; !ok && spec.FrameTimeoutMs != 0 {
		m["frame_timeout_ms"] = spec.FrameTimeoutMs
	}
	return hash, m, nil
}

// yamlToJSON converts a generic YAML-decoded structure (which may carry
// map[any]any) into a JSON-marshallable equivalent.
func yamlToJSON(v any) (any, error) {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("non-string map key %v", k)
			}
			conv, err := yamlToJSON(val)
			if err != nil {
				return nil, err
			}
			out[ks] = conv
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			conv, err := yamlToJSON(val)
			if err != nil {
				return nil, err
			}
			out[k] = conv
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			conv, err := yamlToJSON(val)
			if err != nil {
				return nil, err
			}
			out[i] = conv
		}
		return out, nil
	}
	return v, nil
}
