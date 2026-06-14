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
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/control/config"
)

// Manifest is the on-disk shape of rimsky-compose.yml. Compose is purely
// application-layer: it reconciles templates/tags/instances against an
// already-running rimsky and never starts it, invokes no infra command,
// and materializes no rimsky config (the cut infra/scaffold half lived
// on the removed `dev`/`init` surface).
//
// The optional Executors and ClaimProducers blocks mirror the same-named
// blocks in `rimsky.yml` (see lib/control/config). They are read by the
// `compose run` verb (@decision: services-source) so a manifest is a
// self-contained one-shot input that names the services its nodes
// dispatch to.
type Manifest struct {
	Project        string                                `yaml:"project"`
	Context        string                                `yaml:"context,omitempty"`
	Templates      []TemplateRef                         `yaml:"templates,omitempty"`
	Instances      []InstanceRef                         `yaml:"instances,omitempty"`
	Executors      map[string]ManifestExecutorEntry      `yaml:"executors,omitempty"`
	ClaimProducers map[string]ManifestClaimProducerEntry `yaml:"claim_producers,omitempty"`
}

// ManifestExecutorEntry is one entry in the manifest's `executors:` block,
// mirroring the executors-block shape in `rimsky.yml`. Field names + YAML
// tags match the canonical rimsky.yml schema so the verb can serialize
// the entries verbatim into the synthetic rimsky.yml it writes for the
// in-process role runners.
type ManifestExecutorEntry struct {
	Transport             string   `yaml:"transport"`
	Endpoint              string   `yaml:"endpoint"`
	TLS                   string   `yaml:"tls,omitempty"`
	Protocols             []string `yaml:"protocols,omitempty"`
	ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
}

// ManifestClaimProducerEntry is one entry in the manifest's
// `claim_producers:` block, mirroring the claim_producers-block shape in
// `rimsky.yml`. WriteSemanticsAllowed is required per the rimsky-yml
// concept invariant — the loader rejects the entry without it.
type ManifestClaimProducerEntry struct {
	Endpoint              string   `yaml:"endpoint"`
	Protocols             []string `yaml:"protocols,omitempty"`
	TLS                   string   `yaml:"tls,omitempty"`
	ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
	WriteSemanticsAllowed []string `yaml:"write_semantics_allowed"`
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
	projectRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	instanceRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	tagRe         = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`)
	hashRe        = regexp.MustCompile(`^sha256-[0-9a-f]{64}$`)
	contextRe     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)
	serviceNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

// validExecutorTransport names the transports an executor entry's
// `transport:` field may carry. Matches the rimsky.yml loader's accepted
// set.
var validExecutorTransport = map[string]bool{
	"grpc": true,
	"http": true,
}

// validTLSMode names the values an executor or claim-producer entry's
// `tls:` field may carry when set.
var validTLSMode = map[string]bool{
	"off":      true,
	"required": true,
}

// validWriteSemantics names the values a claim-producer entry's
// `write_semantics_allowed:` list may contain. Matches the rimsky.yml
// loader's accepted set.
var validWriteSemantics = map[string]bool{
	"sync":           true,
	"staged_async":   true,
	"blocking_async": true,
	"read_only":      true,
}

// validProtocols names the values a service entry's `protocols:` list
// may contain. Sourced from config.ValidProtocols so the manifest
// validator's accepted set is identical to the rimsky.yml loader's;
// without this single source, a new protocol added to the loader
// would be silently rejected here at compose-run flag-parse time.
var validProtocols = config.ValidProtocols()

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

	// @constraint: sort the map keys so error order is stable across
	// invocations — Go map iteration is randomized; the operator-facing
	// errors.Join output should be reproducible for CI diffs.
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

// validateExecutorEntry checks transport, endpoint, tls, and protocols
// for a single executor entry. Returns the list of failures; the caller
// folds them into the joined error. Shared by Manifest.Validate (the
// operator-facing parse-time check) and WriteSyntheticRimskyYAML (a
// post-merge belt-and-braces check so a programming error in the
// spawn-overlay constructor cannot produce a synthetic rimsky.yml that
// the role-runner config loader rejects at boot time with a confusing
// deep-stack error).
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

// validateClaimProducerEntry checks endpoint, write_semantics_allowed,
// tls, and protocols for a single claim-producer entry. The
// protocols check matches the rimsky.yml loader's accepted set so an
// unknown protocol surfaces at compose-run flag-parse time rather than
// from the role-runner's persistence Open path at boot.
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

// SiblingRimskyYMLPath returns the absolute path of a sibling
// rimsky.yml next to the manifest if one exists, otherwise the empty
// string. The `compose run` verb uses this to fold publisher and
// named-lock blocks through from a sibling rimsky.yml when the
// manifest itself does not name them (@decision: services-source).
//
// Returns ("", err) only when a sibling file exists but Abs cannot
// resolve it (the underlying os.Getwd failure that filepath.Abs
// surfaces) — silently returning the relative path would dereference
// a different file from a non-cwd-relative caller.
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
