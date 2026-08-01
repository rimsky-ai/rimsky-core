// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type Action string

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

type Kind string

const (
	KindTemplate Kind = "template"
	KindTag      Kind = "tag"
	KindInstance Kind = "instance"
)

type Step struct {
	Action       Action         `json:"action"`
	Kind         Kind           `json:"kind"`
	Tag          string         `json:"tag,omitempty"`
	TemplateHash string         `json:"template_hash,omitempty"`
	TemplateTag  string         `json:"template_tag,omitempty"`
	InstanceID   string         `json:"instance_id,omitempty"`
	InstanceKey  string         `json:"instance_key,omitempty"`
	FromPath     string         `json:"from,omitempty"`
	Source       string         `json:"source,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	Note         string         `json:"note,omitempty"`

	Destructive bool `json:"destructive,omitempty"`

	SpecBody *node.TemplateSpec `json:"-"`
}

type Summary struct {
	Changes int `json:"changes"`
}

type Plan struct {
	Project          string  `json:"project"`
	Context          string  `json:"context,omitempty"`
	Steps            []Step  `json:"plan"`
	Summary          Summary `json:"summary"`
	HasDriftWarnings bool    `json:"has_drift_warnings,omitempty"`
}

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

func ComputePlan(ctx context.Context, c *cli.Client, m *Manifest, state *ComposeState) (*Plan, error) {
	plan := &Plan{
		Project: m.Project,
		Context: m.Context,
	}

	manifestTags := map[string]TemplateRef{}
	for _, t := range m.Templates {
		manifestTags[m.PrefixedTag(t.Tag)] = t
	}
	manifestNames := map[string]InstanceRef{}
	for _, inst := range m.Instances {
		manifestNames[m.PrefixedInstanceKey(inst.Name)] = inst
	}

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

	resolved := map[string]string{}
	specBodies := map[string]node.TemplateSpec{}
	for _, t := range m.Templates {
		hash, spec, err := ResolveTemplate(t.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", t.Path, err)
		}
		resolved[m.PrefixedTag(t.Tag)] = hash
		specBodies[hash] = spec
	}

	registers := []Step{}
	registered := map[string]bool{}
	for _, t := range m.Templates {
		ptag := m.PrefixedTag(t.Tag)
		hash := resolved[ptag]
		if registered[hash] {
			continue
		}
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

	tagSteps := []Step{}
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
			if old, ok := state.TemplatesByH[current.TemplateHash]; ok && old.State == "deployed" {
				oldHashesNeedingUndeploy[current.TemplateHash] = true
			}
			oldHashesNeedingDelete[current.TemplateHash] = true
		}
	}

	deploys := []Step{}
	for _, t := range m.Templates {
		ptag := m.PrefixedTag(t.Tag)
		newHash := resolved[ptag]
		if t.EffectiveState() != "deployed" {
			continue
		}
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

	instanceDeletes := []Step{}
	nonTerminalOrphans := []string{}
	willRecreate := map[string]InstanceRef{}
	for key, inst := range stateInstancesByKey {
		mInst, kept := manifestNames[key]
		if !kept {
			if inst.TerminatedAt == nil {
				nonTerminalOrphans = append(nonTerminalOrphans, key)
				continue
			}
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
			continue
		}
		policy := mInst.EffectiveRestart()
		recreate, deleteOnly, outcome, err := classifyRestart(ctx, c, inst, policy)
		if err != nil {
			return nil, err
		}
		if deleteOnly || recreate {
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
	sort.Slice(instanceDeletes, func(i, j int) bool { return instanceDeletes[i].InstanceKey < instanceDeletes[j].InstanceKey })

	keptHashes := map[string]bool{}
	for _, hash := range resolved {
		keptHashes[hash] = true
	}
	for hash := range oldHashesNeedingUndeploy {
		if keptHashes[hash] {
			delete(oldHashesNeedingUndeploy, hash)
		}
	}
	for hash := range oldHashesNeedingDelete {
		if keptHashes[hash] {
			delete(oldHashesNeedingDelete, hash)
		}
	}

	undeploys := []Step{}
	for hash := range oldHashesNeedingUndeploy {
		undeploys = append(undeploys, Step{
			Action:       ActionUndeploy,
			Kind:         KindTemplate,
			TemplateHash: hash,
		})
	}
	sort.Slice(undeploys, func(i, j int) bool { return undeploys[i].TemplateHash < undeploys[j].TemplateHash })

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
			if !paramsEqual(stateInst.Params, inst.Params) {
				fmt.Fprintf(os.Stderr,
					"warning: params drift on running instance %s; no-op (control-api has no in-flight update)\n",
					key)
				plan.HasDriftWarnings = true
			}
			continue
		}
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

func (m *Manifest) ResolveAndPrefix(ref string) string {
	resolved, _ := m.ResolveTemplateRef(ref)
	return resolved
}

func classifyRestart(ctx context.Context, c *cli.Client, inst cli.Instance, policy string) (recreate, deleteOnly bool, outcome string, err error) {
	outcome, err = aggregateOutcome(ctx, c, inst.UUID())
	if err != nil {
		return false, false, "", err
	}
	switch policy {
	case "always":
		return true, false, outcome, nil
	case "on_failure":
		if outcome == "failure" {
			return true, false, outcome, nil
		}
		return false, true, outcome, nil
	case "never":
		return false, true, outcome, nil
	}
	return false, true, outcome, nil
}

// @concept: node-run
func aggregateOutcome(ctx context.Context, c *cli.Client, instanceID string) (string, error) {
	resp, err := c.ListInstanceNodes(ctx, instanceID)
	if err != nil {
		return "", err
	}
	for _, n := range resp.Nodes {
		s := n.RunSummary
		if s == nil {
			return "failure", nil
		}
		if s.FailedCount > 0 || s.ActiveCount > 0 || s.PendingCount > 0 {
			return "failure", nil
		}
		if s.FreshCount == 0 {
			return "failure", nil
		}
	}
	return "success", nil
}

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
