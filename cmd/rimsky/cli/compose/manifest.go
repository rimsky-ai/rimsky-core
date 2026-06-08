// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// manifest.go — rimsky-compose.yml shape and local validation.
//
// Validation rules per spec §2.8: every failure is collected and
// reported via errors.Join so multi-error reports are surfaced in one
// call.
package compose

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// Manifest is the on-disk shape of rimsky-compose.yml. Compose is purely
// application-layer: it reconciles templates/tags/instances against an
// already-running rimsky and never starts it, invokes no infra command,
// and materializes no rimsky config (the cut infra/scaffold half lived
// on the removed `dev`/`init` surface).
type Manifest struct {
	Project   string        `yaml:"project"`
	Context   string        `yaml:"context,omitempty"`
	Templates []TemplateRef `yaml:"templates,omitempty"`
	Instances []InstanceRef `yaml:"instances,omitempty"`
}

// TemplateRef is one entry of manifest.templates[].
type TemplateRef struct {
	Path  string `yaml:"path"`
	Tag   string `yaml:"tag"`
	State string `yaml:"state,omitempty"`
}

// InstanceRef is one entry of manifest.instances[].
type InstanceRef struct {
	Template string         `yaml:"template"`
	Name     string         `yaml:"name"`
	Params   map[string]any `yaml:"params,omitempty"`
	Restart  string         `yaml:"restart,omitempty"`
}

// LoadManifest reads path, parses it as YAML, and runs Validate. The
// returned error is a errors.Join of every validation failure plus any
// I/O / parse error.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

var (
	projectRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	instanceRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	tagRe      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`)
	hashRe     = regexp.MustCompile(`^sha256-[0-9a-f]{64}$`)
	contextRe  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)
)

// validRestart is the set of allowed restart strings per spec §2.7.
var validRestart = map[string]bool{
	"never":      true,
	"on_failure": true,
	"always":     true,
}

// validTemplateState is the set of allowed state strings per spec §2.6.
var validTemplateState = map[string]bool{
	"registered": true,
	"deployed":   true,
}

// Validate enforces every rule from spec §2.8. The returned error is a
// errors.Join over all failures.
func (m *Manifest) Validate() error {
	var errs []error

	if m.Project == "" {
		errs = append(errs, errors.New("project: required"))
	} else if !projectRe.MatchString(m.Project) {
		errs = append(errs, fmt.Errorf("project: %q does not match %s", m.Project, projectRe.String()))
	}

	if m.Context != "" && !contextRe.MatchString(m.Context) {
		errs = append(errs, fmt.Errorf("context: %q does not match %s", m.Context, contextRe.String()))
	}

	pathSeen := map[string]int{}
	tagSeen := map[string]int{}
	for i, t := range m.Templates {
		if t.Path == "" {
			errs = append(errs, fmt.Errorf("templates[%d].path: required", i))
		} else if prev, ok := pathSeen[t.Path]; ok {
			errs = append(errs, fmt.Errorf("templates[%d].path: duplicate of templates[%d]", i, prev))
		} else {
			pathSeen[t.Path] = i
		}
		if t.Tag == "" {
			errs = append(errs, fmt.Errorf("templates[%d].tag: required", i))
		} else {
			if !tagRe.MatchString(t.Tag) || hashRe.MatchString(t.Tag) {
				errs = append(errs, fmt.Errorf("templates[%d].tag: %q is not a valid tag identifier", i, t.Tag))
			}
			if strings.HasPrefix(t.Tag, cli.ReservedTagPrefix) {
				errs = append(errs, fmt.Errorf("templates[%d].tag: %q uses reserved prefix %q (added automatically)", i, t.Tag, cli.ReservedTagPrefix))
			}
			if prev, ok := tagSeen[t.Tag]; ok {
				errs = append(errs, fmt.Errorf("templates[%d].tag: duplicate of templates[%d]", i, prev))
			} else {
				tagSeen[t.Tag] = i
			}
		}
		state := t.State
		if state == "" {
			state = "deployed"
		}
		if !validTemplateState[state] {
			errs = append(errs, fmt.Errorf("templates[%d].state: %q must be one of registered|deployed", i, state))
		}
	}

	nameSeen := map[string]int{}
	for i, inst := range m.Instances {
		if inst.Name == "" {
			errs = append(errs, fmt.Errorf("instances[%d].name: required", i))
		} else if !instanceRe.MatchString(inst.Name) {
			errs = append(errs, fmt.Errorf("instances[%d].name: %q does not match %s", i, inst.Name, instanceRe.String()))
		}
		if prev, ok := nameSeen[inst.Name]; ok && inst.Name != "" {
			errs = append(errs, fmt.Errorf("instances[%d].name: duplicate of instances[%d]", i, prev))
		} else if inst.Name != "" {
			nameSeen[inst.Name] = i
		}
		if inst.Template == "" {
			errs = append(errs, fmt.Errorf("instances[%d].template: required", i))
		} else if !hashRe.MatchString(inst.Template) {
			if _, ok := tagSeen[inst.Template]; !ok {
				errs = append(errs, fmt.Errorf("instances[%d].template: %q is neither a manifest tag nor a hash", i, inst.Template))
			}
		}
		restart := inst.Restart
		if restart == "" {
			restart = "never"
		}
		if !validRestart[restart] {
			errs = append(errs, fmt.Errorf("instances[%d].restart: %q must be one of never|on_failure|always", i, restart))
		}
	}

	return errors.Join(errs...)
}

// EffectiveState returns the manifest-declared state, defaulting to
// "deployed" when unset.
func (t TemplateRef) EffectiveState() string {
	if t.State == "" {
		return "deployed"
	}
	return t.State
}

// EffectiveRestart returns the manifest-declared restart policy,
// defaulting to "never" when unset.
func (i InstanceRef) EffectiveRestart() string {
	if i.Restart == "" {
		return "never"
	}
	return i.Restart
}

// PrefixedTag returns the project-prefixed form of a tag (compose:<project>:<tag>).
func (m *Manifest) PrefixedTag(tag string) string {
	return cli.ReservedTagPrefix + m.Project + ":" + tag
}

// PrefixedInstanceKey returns compose:<project>:<name>.
func (m *Manifest) PrefixedInstanceKey(name string) string {
	return cli.ReservedTagPrefix + m.Project + ":" + name
}

// ResolveTemplateRef classifies a manifest's instance template field
// into either a manifest-tag or a hash. The returned `resolved` is the
// project-prefixed tag (for tag refs) or the bare hash; kind is "tag"
// or "hash". Unknown manifest tags trigger a Validate failure earlier;
// this helper assumes a previously-validated manifest.
func (m *Manifest) ResolveTemplateRef(ref string) (resolved, kind string) {
	if hashRe.MatchString(ref) {
		return ref, "hash"
	}
	return m.PrefixedTag(ref), "tag"
}
