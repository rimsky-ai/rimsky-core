// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// down.go — `compose down`. Reverses the application-state portion of
// the manifest: instance deletes → undeploys → tag deletes → best-effort
// template deletes. Optionally runs infra.down.command at the end with
// --infra.
package compose

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fallguy/rimsky/modeling/cli"
)

// ComputeDownPlan produces the §3.7 sequence: instance deletes → template
// undeploys → tag deletes → best-effort template deletes. Refuses with
// *ErrComposePlan if any non-terminal compose-owned instances exist.
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

// composeDownFlags collects parsed flags for `compose down`.
type composeDownFlags struct {
	manifestPath string
	infra        bool
	common       cli.CommonFlags
}

func parseComposeDownFlags(args []string) (*composeDownFlags, int) {
	fs := flag.NewFlagSet("compose down", flag.ContinueOnError)
	out := &composeDownFlags{}
	fs.StringVar(&out.manifestPath, "f", "rimsky-compose.yml", "manifest path")
	fs.BoolVar(&out.infra, "infra", false, "also run infra.down.command")
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

// RunComposeDown implements `compose down`. Loads the manifest, resolves
// the client, and delegates to runComposeDownWithManifest.
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
	return runComposeDownWithManifest(ctx, m, c, flags)
}

// runComposeDownWithManifest is the shared body of `compose down`.
// Callers (RunComposeDown, RunDevDown) load the manifest once and
// thread it through, avoiding a second LoadManifest pass.
func runComposeDownWithManifest(ctx context.Context, m *Manifest, c *cli.Client, flags *composeDownFlags) int {
	state, err := QueryState(ctx, c, m.Project)
	if err != nil {
		return reportApplyError(err)
	}
	plan, err := ComputeDownPlan(ctx, c, m, state)
	if err != nil {
		return reportPlanError(err)
	}
	// compose down is destructive by definition.
	destructiveSteps := append([]Step(nil), plan.Steps...)
	if !confirmDestructive(flags.common.Yes, os.Stdin, os.Stderr, destructiveSteps) {
		return 2
	}
	if err := ApplyPlan(ctx, c, plan, ApplyOpts{Logger: os.Stdout}); err != nil {
		return reportApplyError(err)
	}
	if flags.infra {
		if m.Infra == nil || m.Infra.Down == nil {
			fmt.Fprintln(os.Stderr, "no infra.down command defined")
			return 1
		}
		manifestDir := filepath.Dir(flags.manifestPath)
		if err := RunInfraDown(ctx, m.Infra, manifestDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Fprintln(os.Stdout, "compose down complete")
	return 0
}

// runCommand executes argv in workdir and inherits stdout/stderr.
func runCommand(ctx context.Context, argv []string, workdir string, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
