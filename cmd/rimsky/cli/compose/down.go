// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func ComputeDownPlan(ctx context.Context, c *cli.Client, m *Manifest, state *ComposeState) (*Plan, error) {
	plan := &Plan{Project: m.Project, Context: m.Context}

	nonTerminal := []string{}
	instanceDeletes := []Step{}
	for _, inst := range state.Instances {
		if inst.InstanceKey == nil {
			continue
		}
		if inst.TerminatedAt == nil {
			nonTerminal = append(nonTerminal, *inst.InstanceKey)
			continue
		}
		instanceDeletes = append(instanceDeletes, Step{
			Action:      ActionInstanceDelete,
			Kind:        KindInstance,
			InstanceID:  inst.UUID(),
			InstanceKey: *inst.InstanceKey,
			Note:        "compose down",
			Destructive: true,
		})
	}
	if len(nonTerminal) > 0 {
		sort.Strings(nonTerminal)
		return nil, &ErrComposePlan{
			NonTerminalInstanceKeys: nonTerminal,
			Detail: "compose down cannot abort running instances; wait for terminal state and re-run: " +
				strings.Join(nonTerminal, ", "),
		}
	}

	undeploys := []Step{}
	tagDeletes := []Step{}
	templateDeletes := []Step{}
	seenHash := map[string]bool{}
	for _, t := range state.Tags {
		tagDeletes = append(tagDeletes, Step{
			Action:       ActionTagDelete,
			Kind:         KindTag,
			Tag:          t.Tag,
			TemplateHash: t.TemplateHash,
			Destructive:  true,
		})
		if seenHash[t.TemplateHash] {
			continue
		}
		seenHash[t.TemplateHash] = true
		if cur, ok := state.TemplatesByH[t.TemplateHash]; ok && cur.State == "deployed" {
			undeploys = append(undeploys, Step{
				Action:       ActionUndeploy,
				Kind:         KindTemplate,
				TemplateHash: t.TemplateHash,
				Destructive:  true,
			})
		}
		templateDeletes = append(templateDeletes, Step{
			Action:       ActionTemplateDelete,
			Kind:         KindTemplate,
			TemplateHash: t.TemplateHash,
			Destructive:  true,
		})
	}
	sort.Slice(instanceDeletes, func(i, j int) bool { return instanceDeletes[i].InstanceKey < instanceDeletes[j].InstanceKey })
	sort.Slice(undeploys, func(i, j int) bool { return undeploys[i].TemplateHash < undeploys[j].TemplateHash })
	sort.Slice(tagDeletes, func(i, j int) bool { return tagDeletes[i].Tag < tagDeletes[j].Tag })
	sort.Slice(templateDeletes, func(i, j int) bool { return templateDeletes[i].TemplateHash < templateDeletes[j].TemplateHash })

	all := []Step{}
	all = append(all, instanceDeletes...)
	all = append(all, undeploys...)
	all = append(all, tagDeletes...)
	all = append(all, templateDeletes...)
	plan.Steps = all
	plan.Summary.Changes = len(all)
	return plan, nil
}

type composeDownFlags struct {
	manifestPath string
	common       cli.CommonFlags
}

func parseComposeDownFlags(args []string) (*composeDownFlags, int) {
	fs := flag.NewFlagSet("compose down", flag.ContinueOnError)
	out := &composeDownFlags{}
	fs.StringVar(&out.manifestPath, "f", "rimsky-compose.yml", "manifest path")
	cli.RegisterCommonFlags(fs, &out.common)
	if err := fs.Parse(args); err != nil {
		return nil, 2
	}
	if err := out.common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, 2
	}
	cli.SetActiveCommonFlags(&out.common)
	return out, 0
}

func RunComposeDown(ctx context.Context, args []string) int {
	flags, code := parseComposeDownFlags(args)
	if code != 0 {
		return code
	}
	m, err := LoadManifest(flags.manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfgPath, _ := cli.DefaultConfigPath()
	endpoint, err := cli.ResolveEndpointForCompose(flags.common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, m.Context)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := cli.NewClient(endpoint)
	c.SetComposeOrigin(true)
	return runComposeDownWithManifest(ctx, m, c, flags)
}

func runComposeDownWithManifest(ctx context.Context, m *Manifest, c *cli.Client, flags *composeDownFlags) int {
	state, err := QueryState(ctx, c, m.Project)
	if err != nil {
		return reportApplyError(err)
	}
	plan, err := ComputeDownPlan(ctx, c, m, state)
	if err != nil {
		return reportPlanError(err)
	}
	destructiveSteps := append([]Step(nil), plan.Steps...)
	if !confirmDestructive(flags.common.Yes, os.Stdin, os.Stderr, destructiveSteps) {
		return 2
	}
	if _, err := ApplyPlan(ctx, c, plan, ApplyOpts{Logger: os.Stdout}); err != nil {
		return reportApplyError(err)
	}
	fmt.Fprintln(os.Stdout, "compose down complete")
	return 0
}
