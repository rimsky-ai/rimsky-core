// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
)

type Manifest struct {
	Project        string                                `yaml:"project"`
	Context        string                                `yaml:"context,omitempty"`
	Templates      []TemplateRef                         `yaml:"templates,omitempty"`
	Instances      []InstanceRef                         `yaml:"instances,omitempty"`
	Executors      map[string]ManifestExecutorEntry      `yaml:"executors,omitempty"`
	ClaimProducers map[string]ManifestClaimProducerEntry `yaml:"claim_producers,omitempty"`
}

type ManifestExecutorEntry struct {
	Transport             string   `yaml:"transport"`
	Endpoint              string   `yaml:"endpoint"`
	TLS                   string   `yaml:"tls,omitempty"`
	Protocols             []string `yaml:"protocols,omitempty"`
	ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
}

type ManifestClaimProducerEntry struct {
	Endpoint              string   `yaml:"endpoint"`
	Protocols             []string `yaml:"protocols,omitempty"`
	TLS                   string   `yaml:"tls,omitempty"`
	ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
	WriteSemanticsAllowed []string `yaml:"write_semantics_allowed"`
}

type TemplateRef struct {
	Path  string `yaml:"path"`
	Tag   string `yaml:"tag"`
	State string `yaml:"state,omitempty"`
}

type InstanceRef struct {
	Template string         `yaml:"template"`
	Name     string         `yaml:"name"`
	Params   map[string]any `yaml:"params,omitempty"`
	Restart  string         `yaml:"restart,omitempty"`
}

func LoadManifest(path string) (*Manifest, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	var m Manifest
	if err := configload.LoadFile(path, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

var identifierRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

var (
	projectRe     = identifierRe
	instanceRe    = identifierRe
	serviceNameRe = identifierRe
	tagRe         = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`)
	hashRe        = regexp.MustCompile(`^sha256-[0-9a-f]{64}$`)
	contextRe     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)
)

var validExecutorTransport = map[string]bool{
	"grpc": true,
	"http": true,
}

var validTLSMode = map[string]bool{
	"off":      true,
	"required": true,
}

var validWriteSemantics = map[string]bool{
	"sync":           true,
	"staged_async":   true,
	"blocking_async": true,
	"read_only":      true,
}

var validProtocols = config.ValidProtocols()

var validRestart = map[string]bool{
	"never":      true,
	"on_failure": true,
	"always":     true,
}

var validTemplateState = map[string]bool{
	"registered": true,
	"deployed":   true,
}

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

	execNames := make([]string, 0, len(m.Executors))
	for name := range m.Executors {
		execNames = append(execNames, name)
	}
	sort.Strings(execNames)
	for _, name := range execNames {
		e := m.Executors[name]
		if !serviceNameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("executors[%q]: service name does not match %s", name, serviceNameRe.String()))
		}
		if vErrs := validateExecutorEntry(name, e); len(vErrs) > 0 {
			errs = append(errs, vErrs...)
		}
	}

	cpNames := make([]string, 0, len(m.ClaimProducers))
	for name := range m.ClaimProducers {
		cpNames = append(cpNames, name)
	}
	sort.Strings(cpNames)
	for _, name := range cpNames {
		e := m.ClaimProducers[name]
		if !serviceNameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("claim_producers[%q]: service name does not match %s", name, serviceNameRe.String()))
		}
		if vErrs := validateClaimProducerEntry(name, e); len(vErrs) > 0 {
			errs = append(errs, vErrs...)
		}
	}

	return errors.Join(errs...)
}

func validateExecutorEntry(name string, e ManifestExecutorEntry) []error {
	var errs []error
	if e.Transport == "" {
		errs = append(errs, fmt.Errorf("executors[%q].transport: required", name))
	} else if !validExecutorTransport[e.Transport] {
		errs = append(errs, fmt.Errorf("executors[%q].transport: %q must be one of grpc|http", name, e.Transport))
	}
	if e.Endpoint == "" {
		errs = append(errs, fmt.Errorf("executors[%q].endpoint: required", name))
	}
	if e.TLS != "" && !validTLSMode[e.TLS] {
		errs = append(errs, fmt.Errorf("executors[%q].tls: %q must be one of off|required", name, e.TLS))
	}
	for i, p := range e.Protocols {
		if !validProtocols[p] {
			errs = append(errs, fmt.Errorf("executors[%q].protocols[%d]: %q is not a known protocol", name, i, p))
		}
	}
	return errs
}

func validateClaimProducerEntry(name string, e ManifestClaimProducerEntry) []error {
	var errs []error
	if e.Endpoint == "" {
		errs = append(errs, fmt.Errorf("claim_producers[%q].endpoint: required", name))
	}
	if len(e.WriteSemanticsAllowed) == 0 {
		errs = append(errs, fmt.Errorf("claim_producers[%q].write_semantics_allowed: required", name))
	} else {
		for i, v := range e.WriteSemanticsAllowed {
			if !validWriteSemantics[v] {
				errs = append(errs, fmt.Errorf("claim_producers[%q].write_semantics_allowed[%d]: %q must be one of sync|staged_async|blocking_async|read_only", name, i, v))
			}
		}
	}
	if e.TLS != "" && !validTLSMode[e.TLS] {
		errs = append(errs, fmt.Errorf("claim_producers[%q].tls: %q must be one of off|required", name, e.TLS))
	}
	for i, p := range e.Protocols {
		if !validProtocols[p] {
			errs = append(errs, fmt.Errorf("claim_producers[%q].protocols[%d]: %q is not a known protocol", name, i, p))
		}
	}
	return errs
}

func (t TemplateRef) EffectiveState() string {
	if t.State == "" {
		return "deployed"
	}
	return t.State
}

func (i InstanceRef) EffectiveRestart() string {
	if i.Restart == "" {
		return "never"
	}
	return i.Restart
}

func (m *Manifest) PrefixedTag(tag string) string {
	return cli.ReservedTagPrefix + m.Project + ":" + tag
}

func (m *Manifest) PrefixedInstanceKey(name string) string {
	return cli.ReservedTagPrefix + m.Project + ":" + name
}

func SiblingRimskyYMLPath(manifestPath string) (string, error) {
	dir := filepath.Dir(manifestPath)
	candidate := filepath.Join(dir, "rimsky.yml")
	if _, err := os.Stat(candidate); err != nil {
		return "", nil
	}
	abs, aerr := filepath.Abs(candidate)
	if aerr != nil {
		return "", fmt.Errorf("resolve absolute sibling rimsky.yml path %q: %w", candidate, aerr)
	}
	return abs, nil
}

func (m *Manifest) ResolveTemplateRef(ref string) (resolved, kind string) {
	if hashRe.MatchString(ref) {
		return ref, "hash"
	}
	return m.PrefixedTag(ref), "tag"
}
