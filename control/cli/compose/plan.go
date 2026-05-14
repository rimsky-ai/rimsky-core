// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// plan.go — compute a plan that reconciles the manifest with the
// control-api's current state.
//
// Plan steps are ordered serially per spec §3.3:
//  1. template registers
//  2. tag creates / moves
//  3. template deploys
//  4. instance deletes
//  5. template undeploys
//  6. tag deletes
//  7. instance creates
//  8. template deletes (best-effort)
package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fallguy/rimsky/control/cli"
	"github.com/fallguy/rimsky/graph/node"
)

// Action is the verb a plan step performs against the control-api.
type Action string

// String-typed Action constants. Spec §3.2 names: register, tag, deploy,
// create. Other strings extend the vocabulary for steps the example
// JSON doesn't enumerate.
const (
	ActionRegister       Action = "register"
	ActionTagCreate      Action = "tag"
	ActionTagMove        Action = "tag-move"
	ActionDeploy         Action = "deploy"
	ActionInstanceDelete Action = "instance-delete"
	ActionUndeploy       Action = "undeploy"
	ActionTagDelete      Action = "tag-delete"
	ActionInstanceCreate Action = "create"
	ActionTemplateDelete Action = "template-delete"
)

// Kind is the resource a plan step targets.
type Kind string

// Resource kinds.
const (
	KindTemplate Kind = "template"
	KindTag      Kind = "tag"
	KindInstance Kind = "instance"
)

// Step is one unit of work in a plan.
type Step struct {
	Action       Action `json:"action"`
	Kind         Kind   `json:"kind"`
	Tag          string `json:"tag,omitempty"`
	TemplateHash string `json:"template_hash,omitempty"`
	TemplateTag  string `json:"template_tag,omitempty"`
	InstanceID   string `json:"instance_id,omitempty"`
	InstanceKey  string `json:"instance_key,omitempty"`
	// FromPath is the resolved (possibly absolute) path of the spec
	// file used to compute this step's content hash. Surfaced for
	// the human-readable plan output. Not sent to the control-api;
	// see Source for that.
	FromPath string `json:"from,omitempty"`
	// Source is the friendly identifier sent to the control-api in
	// the register-template body's `source` field. Form is
	// `manifest:<project>:<tag>`. Spec'd to be operator-portable
	// (no $HOME/.../etc absolute paths).
	Source string         `json:"source,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Note   string         `json:"note,omitempty"`

	// Destructive is set at plan time when the step destroys data
	// (deletes, undeploys, or removes a tag pointing at a template
	// referenced by an active workload). The destructive() helper is
	// a one-line check on this field; spec §3.6 makes destructiveness
	// outcome-based, not Note-string-based, so the bool is the
	// authoritative signal.
	Destructive bool `json:"destructive,omitempty"`

	// SpecBody is the typed spec body for register steps. Not emitted
	// to the JSON output to keep plans concise.
	SpecBody *node.TemplateSpec `json:"-"`
}

// Summary annotates a plan with high-level counts.
type Summary struct {
	Changes int `json:"changes"`
}

// Plan is the output of ComputePlan: an ordered sequence of steps.
type Plan struct {
	Project string  `json:"project"`
	Context string  `json:"context,omitempty"`
	Steps   []Step  `json:"plan"`
	Summary Summary `json:"summary"`
	// HasDriftWarnings is true when ComputePlan emitted at least one
	// stderr drift warning (e.g. params drift on a non-terminal compose-
	// owned instance). The control-api has no in-flight update path for
	// such drift, so the plan schedules zero steps for the affected
	// instance — but `compose plan` still exits non-zero (3) so CI
	// gating mirrors `terraform plan -detailed-exitcode`.
	HasDriftWarnings bool `json:"has_drift_warnings,omitempty"`
}

// ErrComposePlan signals plan-time obstacles such as non-terminal
// compose-owned instances missing from the manifest. The wrapped
// instance keys list the offenders.
type ErrComposePlan struct {
	NonTerminalInstanceKeys []string
	Detail                  string
}

func (e *ErrComposePlan) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return "compose plan error: " + strings.Join(e.NonTerminalInstanceKeys, ", ")
}

// ComputePlan diffs manifest vs current state and emits a serially-
// ordered plan. The state argument is what QueryState returns.
func ComputePlan(ctx context.Context, c *cli.Client, m *Manifest, state *ComposeState) (*Plan, error) {
	plan := &Plan{
		Project: m.Project,
		Context: m.Context,
	}

	// Index manifest templates by prefixed-tag for fast lookup.
	manifestTags := map[string]TemplateRef{}
	for _, t := range m.Templates {
		manifestTags[m.PrefixedTag(t.Tag)] = t
	}
	manifestNames := map[string]InstanceRef{}
	for _, inst := range m.Instances {
		manifestNames[m.PrefixedInstanceKey(inst.Name)] = inst
	}

	// Index state by tag name.
	stateTagsByName := map[string]TagWithTemplate{}
	for _, t := range state.Tags {
		stateTagsByName[t.Tag] = t
	}
	stateInstancesByKey := map[string]cli.Instance{}
	for _, inst := range state.Instances {
		if inst.InstanceKey == nil {
			continue
		}
		stateInstancesByKey[*inst.InstanceKey] = inst
	}

	// Resolve template paths → hashes upfront.
	resolved := map[string]string{} // prefixedTag → newHash
	specBodies := map[string]node.TemplateSpec{}
	for _, t := range m.Templates {
		hash, spec, err := ResolveTemplate(t.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", t.Path, err)
		}
		resolved[m.PrefixedTag(t.Tag)] = hash
		specBodies[hash] = spec
	}

	// Step 1: registers (one per unique new hash).
	registers := []Step{}
	registered := map[string]bool{}
	for _, t := range m.Templates {
		ptag := m.PrefixedTag(t.Tag)
		hash := resolved[ptag]
		if registered[hash] {
			continue
		}
		// Skip register if the target hash is already present in state.
		if _, exists := state.TemplatesByH[hash]; exists {
			registered[hash] = true
			continue
		}
		registered[hash] = true
		specCopy := specBodies[hash]
		registers = append(registers, Step{
			Action:       ActionRegister,
			Kind:         KindTemplate,
			TemplateHash: hash,
			FromPath:     t.Path,
			Source:       fmt.Sprintf("manifest:%s:%s", m.Project, t.Tag),
			SpecBody:     &specCopy,
		})
	}

	// Step 2: tag creates and moves.
	tagSteps := []Step{}
	// Track which tags have a hash mismatch so we can sequence the old
	// hash's undeploy / delete in the later steps.
	oldHashesNeedingUndeploy := map[string]bool{}
	oldHashesNeedingDelete := map[string]bool{}
	for _, t := range m.Templates {
		ptag := m.PrefixedTag(t.Tag)
		newHash := resolved[ptag]
		current, ok := stateTagsByName[ptag]
		if !ok {
			tagSteps = append(tagSteps, Step{
				Action:       ActionTagCreate,
				Kind:         KindTag,
				Tag:          ptag,
				TemplateHash: newHash,
			})
			continue
		}
		if current.TemplateHash != newHash {
			tagSteps = append(tagSteps, Step{
				Action:       ActionTagMove,
				Kind:         KindTag,
				Tag:          ptag,
				TemplateHash: newHash,
				Note:         "from " + cli.TruncHash(current.TemplateHash),
			})
			// Old hash needs cleanup: undeploy if currently deployed,
			// then delete.
			if old, ok := state.TemplatesByH[current.TemplateHash]; ok && old.State == "deployed" {
				oldHashesNeedingUndeploy[current.TemplateHash] = true
			}
			oldHashesNeedingDelete[current.TemplateHash] = true
		}
	}

	// Step 3: deploys (manifest state == deployed AND control-api !=
	// deployed). Look up via ListTemplates view in state — but we only
	// have hashes referenced by owned tags. After register, the new
	// hash isn't in state.TemplatesByH; we treat the new hash as needing
	// deploy if manifest declares so.
	deploys := []Step{}
	for _, t := range m.Templates {
		ptag := m.PrefixedTag(t.Tag)
		newHash := resolved[ptag]
		if t.EffectiveState() != "deployed" {
			continue
		}
		// If the hash already exists in state and is deployed, skip.
		if cur, ok := state.TemplatesByH[newHash]; ok && cur.State == "deployed" {
			continue
		}
		deploys = append(deploys, Step{
			Action:       ActionDeploy,
			Kind:         KindTemplate,
			TemplateHash: newHash,
			Tag:          ptag,
		})
	}
	// If manifest says "registered" and control-api state is deployed:
	// schedule an undeploy.
	for _, t := range m.Templates {
		ptag := m.PrefixedTag(t.Tag)
		newHash := resolved[ptag]
		if t.EffectiveState() != "registered" {
			continue
		}
		if cur, ok := state.TemplatesByH[newHash]; ok && cur.State == "deployed" {
			oldHashesNeedingUndeploy[newHash] = true
		}
	}

	// Manifest tags removed: undeploy + tag delete + best-effort template
	// delete.
	removedTags := []TagWithTemplate{}
	for _, t := range state.Tags {
		if _, kept := manifestTags[t.Tag]; !kept {
			removedTags = append(removedTags, t)
			if old, ok := state.TemplatesByH[t.TemplateHash]; ok && old.State == "deployed" {
				oldHashesNeedingUndeploy[t.TemplateHash] = true
			}
			oldHashesNeedingDelete[t.TemplateHash] = true
		}
	}

	// Step 4: instance deletes.
	instanceDeletes := []Step{}
	// For each manifest instance: if a terminal row already exists for
	// the same key, schedule delete (so step 7 can recreate).
	// For each compose-owned instance not in the manifest:
	//   non-terminal → error;
	//   terminal → schedule delete.
	nonTerminalOrphans := []string{}
	willRecreate := map[string]InstanceRef{}
	for key, inst := range stateInstancesByKey {
		mInst, kept := manifestNames[key]
		if !kept {
			if inst.TerminatedAt == nil {
				nonTerminalOrphans = append(nonTerminalOrphans, key)
				continue
			}
			// Failure-outcome orphan deletes destroy data the
			// operator may want to inspect; mark destructive.
			outcome, err := aggregateOutcome(ctx, c, inst.UUID())
			if err != nil {
				return nil, err
			}
			instanceDeletes = append(instanceDeletes, Step{
				Action:      ActionInstanceDelete,
				Kind:        KindInstance,
				InstanceID:  inst.UUID(),
				InstanceKey: key,
				Note:        "terminal; not in manifest",
				Destructive: outcome == "failure",
			})
			continue
		}
		if inst.TerminatedAt == nil {
			// Non-terminal manifest entry: leave alone.
			continue
		}
		// Terminal row + manifest entry: apply restart policy.
		policy := mInst.EffectiveRestart()
		recreate, deleteOnly, outcome, err := classifyRestart(ctx, c, inst, policy)
		if err != nil {
			return nil, err
		}
		if deleteOnly || recreate {
			// Failure-outcome terminal deletes destroy data the
			// operator may want to inspect; require --yes / a TTY
			// confirmation. Success-outcome terminal cleanup is
			// non-destructive.
			instanceDeletes = append(instanceDeletes, Step{
				Action:      ActionInstanceDelete,
				Kind:        KindInstance,
				InstanceID:  inst.UUID(),
				InstanceKey: key,
				Note:        "restart=" + policy,
				Destructive: outcome == "failure",
			})
		}
		if recreate {
			willRecreate[key] = mInst
		}
	}
	if len(nonTerminalOrphans) > 0 {
		sort.Strings(nonTerminalOrphans)
		return nil, &ErrComposePlan{
			NonTerminalInstanceKeys: nonTerminalOrphans,
			Detail: fmt.Sprintf("compose-owned non-terminal instances not in manifest: %s "+
				"(wait for terminal state and re-run, or invalidate manually)",
				strings.Join(nonTerminalOrphans, ", ")),
		}
	}

	// Step 5: undeploys (sorted for determinism).
	undeploys := []Step{}
	for hash := range oldHashesNeedingUndeploy {
		undeploys = append(undeploys, Step{
			Action:       ActionUndeploy,
			Kind:         KindTemplate,
			TemplateHash: hash,
		})
	}
	sort.Slice(undeploys, func(i, j int) bool { return undeploys[i].TemplateHash < undeploys[j].TemplateHash })

	// Step 6: tag deletes (manifest-removed tags).
	tagDeletes := []Step{}
	for _, t := range removedTags {
		tagDeletes = append(tagDeletes, Step{
			Action:       ActionTagDelete,
			Kind:         KindTag,
			Tag:          t.Tag,
			TemplateHash: t.TemplateHash,
		})
	}
	sort.Slice(tagDeletes, func(i, j int) bool { return tagDeletes[i].Tag < tagDeletes[j].Tag })

	// Step 7: instance creates (new + recreated).
	instanceCreates := []Step{}
	manifestKeys := make([]string, 0, len(m.Instances))
	for _, inst := range m.Instances {
		manifestKeys = append(manifestKeys, m.PrefixedInstanceKey(inst.Name))
	}
	sort.Strings(manifestKeys)
	for _, key := range manifestKeys {
		inst := manifestNames[key]
		stateInst, ok := stateInstancesByKey[key]
		if ok && stateInst.TerminatedAt == nil {
			// Spec §3.1: params drift on a running instance is a
			// warning (control-api has no in-flight update). Print
			// it to stderr at plan time and continue — there is no
			// step to schedule. Setting HasDriftWarnings causes
			// `compose plan` to exit 3 even though Steps is empty,
			// matching terraform plan -detailed-exitcode semantics.
			if !paramsEqual(stateInst.Params, inst.Params) {
				fmt.Fprintf(os.Stderr,
					"warning: params drift on running instance %s; no-op (control-api has no in-flight update)\n",
					key)
				plan.HasDriftWarnings = true
			}
			continue
		}
		// Terminal row will be deleted in step 4 (delete-only or recreate).
		// Recreate when willRecreate covers it OR no row at all.
		if ok && stateInst.TerminatedAt != nil {
			if _, recreate := willRecreate[key]; !recreate {
				continue
			}
		}
		instanceCreates = append(instanceCreates, Step{
			Action:      ActionInstanceCreate,
			Kind:        KindInstance,
			InstanceKey: key,
			TemplateTag: m.ResolveAndPrefix(inst.Template),
			Params:      inst.Params,
		})
	}

	// Step 8: best-effort template deletes.
	templateDeletes := []Step{}
	for hash := range oldHashesNeedingDelete {
		templateDeletes = append(templateDeletes, Step{
			Action:       ActionTemplateDelete,
			Kind:         KindTemplate,
			TemplateHash: hash,
		})
	}
	sort.Slice(templateDeletes, func(i, j int) bool { return templateDeletes[i].TemplateHash < templateDeletes[j].TemplateHash })

	all := []Step{}
	all = append(all, registers...)
	all = append(all, tagSteps...)
	all = append(all, deploys...)
	all = append(all, instanceDeletes...)
	all = append(all, undeploys...)
	all = append(all, tagDeletes...)
	all = append(all, instanceCreates...)
	all = append(all, templateDeletes...)
	plan.Steps = all
	plan.Summary.Changes = len(all)
	return plan, nil
}

// ResolveAndPrefix returns the project-prefixed form of an instance's
// `template:` field for use in the wire body.
func (m *Manifest) ResolveAndPrefix(ref string) string {
	resolved, _ := m.ResolveTemplateRef(ref)
	return resolved
}

// classifyRestart applies the spec §3.5 restart policy table to a
// terminal instance + manifest entry. Returns recreate=true when a
// new row should follow the delete; deleteOnly=true when only delete
// is scheduled (cleanup of terminal row); both false leaves the row.
// Also returns the aggregate outcome ("success" | "failure") so callers
// can mark the resulting step Destructive without re-querying nodes.
func classifyRestart(ctx context.Context, c *cli.Client, inst cli.Instance, policy string) (recreate, deleteOnly bool, outcome string, err error) {
	outcome, err = aggregateOutcome(ctx, c, inst.UUID())
	if err != nil {
		return false, false, "", err
	}
	switch policy {
	case "always":
		// Recreate on every terminal regardless of outcome.
		return true, false, outcome, nil
	case "on_failure":
		if outcome == "failure" {
			return true, false, outcome, nil
		}
		return false, true, outcome, nil
	case "never":
		return false, true, outcome, nil
	}
	// Unreachable in the current call graph: ComputePlan always passes
	// InstanceRef.EffectiveRestart(), which manifest validation has
	// already normalized to {never|on_failure|always}. Kept as the
	// default for any future restart string that lands in the spec
	// without updating this switch.
	return false, true, outcome, nil
}

// aggregateOutcome inspects an instance's nodes and classifies the
// terminal as "success" or "failure". Per docs/history/2026-05-02-rimsky-cli-and-compose-design.md §3.5,
// success means every node ended in `fresh`; any other state — failed,
// running, stale — counts as failure.
//
// In a properly-terminal instance, blessed-invariant 13 says no node
// will be in `running` or `stale`. The strict-equality check here is
// defensive: if a future change strands a non-fresh node on a
// terminated instance, the CLI must not silently classify it as success
// and schedule a recreate-on-success path against it.
func aggregateOutcome(ctx context.Context, c *cli.Client, instanceID string) (string, error) {
	resp, err := c.ListInstanceNodes(ctx, instanceID)
	if err != nil {
		return "", err
	}
	for _, n := range resp.Nodes {
		if n.State != "fresh" {
			return "failure", nil
		}
	}
	return "success", nil
}

// paramsEqual compares two params maps by round-tripping each through
// `json.Marshal` and comparing the bytes. Round-tripping normalizes
// numeric types: a manifest's `count: 5` (typed as int by the YAML
// decoder) vs. the control-api's round-tripped `count: 5.0` (typed as
// float64 by JSON) compare equal because both stringify identically
// after Marshal. fmt.Sprintf-based comparison is unreliable here:
// fmt prints int(5) as "5" but float64(5.0) as "5", which only
// happens to match for whole numbers — so the previous implementation
// was right by accident on int-vs-float-of-the-same-magnitude and
// wrong on non-integer floats.
func paramsEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	ja, err := json.Marshal(a)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	// Re-decode to strip Go-side type differences (int vs float64) by
	// landing both sides in identical map[string]any shapes; then
	// re-marshal so map key ordering is canonical (Go's json.Marshal
	// sorts map keys).
	var aa, bb any
	if err := json.Unmarshal(ja, &aa); err != nil {
		return false
	}
	if err := json.Unmarshal(jb, &bb); err != nil {
		return false
	}
	ja2, _ := json.Marshal(aa)
	jb2, _ := json.Marshal(bb)
	return string(ja2) == string(jb2)
}
