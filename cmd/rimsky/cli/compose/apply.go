// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

type ApplyOpts struct {
	Logger io.Writer
}

type CreatedInstance struct {
	Key          string
	ID           string
	TemplateHash string
}

func ApplyPlan(ctx context.Context, c *cli.Client, plan *Plan, opts ApplyOpts) ([]CreatedInstance, int, error) {
	w := opts.Logger
	if w == nil {
		w = os.Stdout
	}
	var created []CreatedInstance
	applied := 0
	for _, step := range plan.Steps {
		ci, wasApplied, err := applyStep(ctx, c, step, w, opts)
		if err != nil {
			return created, applied, fmt.Errorf("step %s %s: %w", step.Action, stepTarget(step), err)
		}
		if wasApplied {
			applied++
		}
		if ci != nil {
			created = append(created, *ci)
		}
	}
	return created, applied, nil
}

func stepTarget(s Step) string {
	switch s.Kind {
	case KindTag:
		return s.Tag
	case KindInstance:
		return s.InstanceKey
	default:
		return cli.TruncHash(s.TemplateHash)
	}
}

func applyStep(ctx context.Context, c *cli.Client, step Step, w io.Writer, opts ApplyOpts) (*CreatedInstance, bool, error) {
	logf := func(verb, target, status string) {
		fmt.Fprintf(w, "  %s %s %s\n", verb, target, status)
	}
	switch step.Action {
	case ActionRegister:
		if step.SpecBody == nil {
			return nil, false, fmt.Errorf("register step missing spec body")
		}
		body := cli.RegisterTemplateRequest{Spec: *step.SpecBody, Source: step.Source}
		resp, err := c.RegisterTemplate(ctx, body)
		if err != nil {
			return nil, false, err
		}
		logf("register", cli.TruncHash(resp.Hash()), "ok")
		return nil, true, nil
	case ActionTagCreate:
		if _, err := c.CreateTag(ctx, cli.CreateTagRequest{Tag: step.Tag, Template: step.TemplateHash}); err != nil {
			if cli.IsConflict(err) {
				logf("tag", step.Tag, "skipped (already exists)")
				return nil, false, nil
			}
			return nil, false, err
		}
		logf("tag", step.Tag, "ok")
	case ActionTagMove:
		if _, err := c.MoveTag(ctx, step.Tag, cli.MoveTagRequest{Template: step.TemplateHash}); err != nil {
			return nil, false, err
		}
		logf("tag-move", step.Tag, "ok")
	case ActionDeploy:
		ref := step.TemplateHash
		if ref == "" {
			ref = step.Tag
		}
		if _, err := c.DeployTemplate(ctx, ref); err != nil {
			return nil, false, err
		}
		logf("deploy", ref, "ok")
	case ActionInstanceDelete:
		if err := c.DeleteInstance(ctx, step.InstanceID); err != nil {
			if cli.IsNotFound(err) {
				logf("instance-delete", step.InstanceKey, "skipped (already gone)")
				return nil, false, nil
			}
			return nil, false, err
		}
		logf("instance-delete", step.InstanceKey, "ok")
	case ActionUndeploy:
		if _, err := c.UndeployTemplate(ctx, step.TemplateHash); err != nil {
			if cli.IsConflict(err) {
				logf("undeploy", cli.TruncHash(step.TemplateHash), "skipped (still has active instances or already undeployed)")
				return nil, false, nil
			}
			return nil, false, err
		}
		logf("undeploy", cli.TruncHash(step.TemplateHash), "ok")
	case ActionTagDelete:
		if err := c.DeleteTag(ctx, step.Tag); err != nil {
			if cli.IsNotFound(err) {
				logf("tag-rm", step.Tag, "skipped (already gone)")
				return nil, false, nil
			}
			return nil, false, err
		}
		logf("tag-rm", step.Tag, "ok")
	case ActionInstanceCreate:
		key := step.InstanceKey
		body := cli.CreateInstanceRequest{
			Template:    step.TemplateTag,
			InstanceKey: &key,
			Params:      step.Params,
			TargetAgent: cli.ResolveTargetAgent("", ""),
		}
		resp, err := c.CreateInstance(ctx, body)
		if err != nil {
			return nil, false, err
		}
		logf("create", step.InstanceKey, "ok")
		return &CreatedInstance{Key: step.InstanceKey, ID: resp.UUID(), TemplateHash: resp.TemplateHash}, true, nil
	case ActionTemplateDelete:
		if err := c.DeleteTemplate(ctx, step.TemplateHash); err != nil {
			if cli.IsConflict(err) || cli.IsNotFound(err) {
				logf("template-delete", cli.TruncHash(step.TemplateHash), "skipped (still referenced)")
				return nil, false, nil
			}
			return nil, false, err
		}
		logf("template-delete", cli.TruncHash(step.TemplateHash), "ok")
	default:
		return nil, false, fmt.Errorf("unknown action %s", step.Action)
	}
	return nil, true, nil
}

func EmitPlan(w io.Writer, plan *Plan, format cli.Format) {
	if format == cli.FormatJSON {
		_ = cli.EmitJSON(w, plan)
		return
	}
	header := fmt.Sprintf("Plan for project=%s", plan.Project)
	if plan.Context != "" {
		header += fmt.Sprintf(", context=%s", plan.Context)
	}
	fmt.Fprintln(w, header+":")
	if len(plan.Steps) == 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "  no changes")
		return
	}
	groupTpl := []Step{}
	groupTag := []Step{}
	groupInst := []Step{}
	for _, s := range plan.Steps {
		switch s.Kind {
		case KindTemplate:
			groupTpl = append(groupTpl, s)
		case KindTag:
			groupTag = append(groupTag, s)
		case KindInstance:
			groupInst = append(groupInst, s)
		}
	}
	if len(groupTpl) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Templates:")
		for _, s := range groupTpl {
			fmt.Fprintln(w, "  "+formatStep(s))
		}
	}
	if len(groupTag) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Tags:")
		for _, s := range groupTag {
			fmt.Fprintln(w, "  "+formatStep(s))
		}
	}
	if len(groupInst) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Instances:")
		for _, s := range groupInst {
			fmt.Fprintln(w, "  "+formatStep(s))
		}
	}
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "%d changes.\n", plan.Summary.Changes)
}

func formatStep(s Step) string {
	sym, color := "+", cli.AnsiGreen
	switch s.Action {
	case ActionInstanceDelete, ActionUndeploy, ActionTagDelete, ActionTemplateDelete:
		sym, color = "-", cli.AnsiRed
	case ActionTagMove:
		sym, color = "~", ""
	}
	if color != "" {
		sym = cli.Colorize(os.Stdout, color, sym)
	}
	switch s.Kind {
	case KindTag:
		return fmt.Sprintf("%s %s %s → %s", sym, s.Action, s.Tag, cli.TruncHash(s.TemplateHash))
	case KindInstance:
		base := fmt.Sprintf("%s %s %s", sym, s.Action, s.InstanceKey)
		if s.TemplateTag != "" {
			base += fmt.Sprintf(" template=%s", s.TemplateTag)
		}
		if s.Note != "" {
			base += fmt.Sprintf(" (%s)", s.Note)
		}
		return base
	default:
		extra := s.FromPath
		if extra == "" {
			extra = s.Note
		}
		return strings.TrimSpace(fmt.Sprintf("%s %s %s %s", sym, s.Action, cli.TruncHash(s.TemplateHash), extra))
	}
}

func destructive(step Step, undeployHasNonComposeBindings map[string]bool) bool {
	if step.Destructive {
		return true
	}
	if step.Action == ActionUndeploy && undeployHasNonComposeBindings[step.TemplateHash] {
		return true
	}
	return false
}

func precomputeUndeployBindings(ctx context.Context, c *cli.Client, project string, plan *Plan) map[string]bool {
	out := map[string]bool{}
	prefix := cli.ReservedTagPrefix + project + ":"
	seen := map[string]bool{}
	for _, step := range plan.Steps {
		if step.Action != ActionUndeploy {
			continue
		}
		if seen[step.TemplateHash] {
			continue
		}
		seen[step.TemplateHash] = true
		insts, err := cli.PagedListInstances(ctx, c, cli.ListInstancesQuery{TemplateHash: step.TemplateHash})
		if err != nil {
			out[step.TemplateHash] = true
			continue
		}
		for _, inst := range insts {
			if inst.TerminatedAt != nil {
				continue
			}
			if inst.InstanceKey == nil || !strings.HasPrefix(*inst.InstanceKey, prefix) {
				out[step.TemplateHash] = true
				break
			}
		}
	}
	return out
}

func confirmDestructive(yes bool, in io.Reader, out io.Writer, destructiveSteps []Step) bool {
	if len(destructiveSteps) == 0 {
		return true
	}
	if yes {
		return true
	}
	if !isTerminal(in) {
		fmt.Fprintln(out, "destructive operations require --yes:")
		for _, s := range destructiveSteps {
			fmt.Fprintln(out, "  "+formatStep(s))
		}
		return false
	}
	fmt.Fprintln(out, "the following destructive operations are scheduled:")
	for _, s := range destructiveSteps {
		fmt.Fprintln(out, "  "+formatStep(s))
	}
	fmt.Fprintf(out, "Proceed? [y/N] ")
	br := bufio.NewReader(in)
	line, _ := br.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y"
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

type composeUpFlags struct {
	manifestPath string
	common       cli.CommonFlags
}

func parseComposeFlags(name string, args []string) (*composeUpFlags, []string, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	out := &composeUpFlags{}
	fs.StringVar(&out.manifestPath, "f", "rimsky-compose.yml", "path to rimsky-compose.yml")
	cli.RegisterCommonFlags(fs, &out.common)
	if err := fs.Parse(args); err != nil {
		return nil, nil, 2
	}
	if err := out.common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, 2
	}
	if rest := fs.Args(); len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "%s: unexpected positional argument(s): %s\n", name, strings.Join(rest, " "))
		return nil, nil, 2
	}
	cli.SetActiveCommonFlags(&out.common)
	return out, fs.Args(), 0
}

func loadManifestAndClient(flags *composeUpFlags) (*Manifest, *cli.Client, string, int) {
	m, err := LoadManifest(flags.manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", 2
	}
	resolveTemplatePaths(m, flags.manifestPath)
	c, endpoint, code := clientForManifest(flags, m)
	if code != 0 {
		return nil, nil, "", code
	}
	return m, c, endpoint, 0
}

func resolveTemplatePaths(m *Manifest, manifestPath string) {
	mdir := filepath.Dir(manifestPath)
	for i := range m.Templates {
		if !filepath.IsAbs(m.Templates[i].Path) {
			m.Templates[i].Path = filepath.Join(mdir, m.Templates[i].Path)
		}
	}
}

func clientForManifest(flags *composeUpFlags, m *Manifest) (*cli.Client, string, int) {
	cfgPath, _ := cli.DefaultConfigPath()
	endpoint, err := cli.ResolveEndpointForCompose(flags.common.Endpoint, os.Getenv("RIMSKY_CONTROL_API_URL"), cfgPath, m.Context)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, "", 2
	}
	c := cli.NewClient(endpoint)
	c.SetComposeOrigin(true)
	return c, endpoint, 0
}

func RunComposeUp(ctx context.Context, args []string) int {
	flags, _, code := parseComposeFlags("compose up", args)
	if code != 0 {
		return code
	}
	m, c, _, code := loadManifestAndClient(flags)
	if code != 0 {
		return code
	}
	return runComposeUpWithManifest(ctx, m, c, flags)
}

func runComposeUpWithManifest(ctx context.Context, m *Manifest, c *cli.Client, flags *composeUpFlags) int {
	state, err := QueryState(ctx, c, m.Project)
	if err != nil {
		return reportApplyError(err)
	}
	plan, err := ComputePlan(ctx, c, m, state)
	if err != nil {
		return reportPlanError(err)
	}
	undeployBindings := precomputeUndeployBindings(ctx, c, m.Project, plan)
	destructiveSteps := []Step{}
	for _, step := range plan.Steps {
		if destructive(step, undeployBindings) {
			destructiveSteps = append(destructiveSteps, step)
		}
	}
	if !confirmDestructive(flags.common.Yes, os.Stdin, os.Stderr, destructiveSteps) {
		return 2
	}
	_, applied, err := ApplyPlan(ctx, c, plan, ApplyOpts{Logger: os.Stdout})
	if err != nil {
		return reportApplyError(err)
	}
	if applied == 0 {
		fmt.Fprintln(os.Stdout, "no changes")
	} else {
		fmt.Fprintf(os.Stdout, "applied %d changes\n", applied)
	}
	return 0
}

func RunComposePlan(ctx context.Context, args []string) int {
	flags, _, code := parseComposeFlags("compose plan", args)
	if code != 0 {
		return code
	}
	m, c, _, code := loadManifestAndClient(flags)
	if code != 0 {
		return code
	}
	state, err := QueryState(ctx, c, m.Project)
	if err != nil {
		return reportApplyError(err)
	}
	plan, err := ComputePlan(ctx, c, m, state)
	if err != nil {
		return reportPlanError(err)
	}
	EmitPlan(os.Stdout, plan, flags.common.Format)
	if plan.Summary.Changes == 0 && !plan.HasDriftWarnings {
		return 0
	}
	return 3
}

func RunComposeStatus(ctx context.Context, args []string) int {
	flags, _, code := parseComposeFlags("compose status", args)
	if code != 0 {
		return code
	}
	m, c, _, code := loadManifestAndClient(flags)
	if code != 0 {
		return code
	}
	state, err := QueryState(ctx, c, m.Project)
	if err != nil {
		return reportApplyError(err)
	}

	type annot struct {
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		Annotation string `json:"annotation"`
	}
	rows := []annot{}

	manifestTags := map[string]bool{}
	for _, t := range m.Templates {
		manifestTags[m.PrefixedTag(t.Tag)] = true
	}
	manifestNames := map[string]bool{}
	for _, inst := range m.Instances {
		manifestNames[m.PrefixedInstanceKey(inst.Name)] = true
	}
	stateTags := map[string]bool{}
	for _, t := range state.Tags {
		stateTags[t.Tag] = true
	}
	stateInsts := map[string]bool{}
	for _, inst := range state.Instances {
		if inst.InstanceKey != nil {
			stateInsts[*inst.InstanceKey] = true
		}
	}

	tagNames := []string{}
	for k := range manifestTags {
		tagNames = append(tagNames, k)
	}
	for k := range stateTags {
		if _, ok := manifestTags[k]; !ok {
			tagNames = append(tagNames, k)
		}
	}
	sort.Strings(tagNames)
	for _, tag := range tagNames {
		ann := "in-manifest"
		if !stateTags[tag] {
			ann = "manifest-missing-from-api"
		} else if !manifestTags[tag] {
			ann = "api-missing-from-manifest"
		}
		rows = append(rows, annot{Kind: "tag", Name: tag, Annotation: ann})
	}

	instNames := []string{}
	for k := range manifestNames {
		instNames = append(instNames, k)
	}
	for k := range stateInsts {
		if _, ok := manifestNames[k]; !ok {
			instNames = append(instNames, k)
		}
	}
	sort.Strings(instNames)
	for _, key := range instNames {
		ann := "in-manifest"
		if !stateInsts[key] {
			ann = "manifest-missing-from-api"
		} else if !manifestNames[key] {
			ann = "api-missing-from-manifest"
		}
		rows = append(rows, annot{Kind: "instance", Name: key, Annotation: ann})
	}

	if flags.common.Format == cli.FormatJSON {
		_ = cli.EmitJSON(os.Stdout, map[string]any{
			"project": m.Project,
			"items":   rows,
		})
		return 0
	}
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Kind, r.Name, r.Annotation})
	}
	cli.EmitTable(os.Stdout, []string{"KIND", "NAME", "ANNOTATION"}, out)
	return 0
}

func reportPlanError(err error) int {
	// @decision: auth-dry-run-request-flag
	if code, ok := cli.ReportDryRunPreview(err); ok {
		return code
	}
	var perr *ErrComposePlan
	if errors.As(err, &perr) {
		fmt.Fprintln(os.Stderr, perr.Error())
		return 1
	}
	var apiErr *cli.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintln(os.Stderr, apiErr.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func reportApplyError(err error) int {
	// @decision: auth-dry-run-request-flag
	if code, ok := cli.ReportDryRunPreview(err); ok {
		return code
	}
	var apiErr *cli.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintln(os.Stderr, apiErr.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}
