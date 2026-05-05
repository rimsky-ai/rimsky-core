// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// apply.go — execute a Plan serially against the control-api.
//
// Compose-up entry point: parse manifest, query state, compute plan,
// run destructive-op pre-check, apply. Compose-plan: same flow up to
// emit. Compose-status: read-only annotation.
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

	"github.com/fallguy/rimsky/modeling/cli"
)

// ApplyOpts controls how ApplyPlan runs. Confirmation is gated upstream
// in runComposeUpWithManifest / runComposeDownWithManifest via
// confirmDestructive — ApplyPlan does not consult any --yes flag, so
// this struct only carries the writer for step-by-step progress logs.
type ApplyOpts struct {
	Logger io.Writer
}

// ApplyPlan executes plan.Steps serially against c. Returns immediately
// on the first step error, wrapping the failed step.
//
// Pre-2026-05-02 the CLI computed plan-time hashes against the typed
// TemplateSpec while the control-api stored hashes computed against the
// shadow-tree-decoded view; those two could diverge when capital-N
// capital keys leaked into one side's JSON marshal but not the other.
// The 2026-05-02 json-tags cleanup unified them — both sides now hash
// the same lowercase-snake-case bytes — so ApplyPlan no longer needs
// the hash-rewrite defense it carried during the rimsky-cli rollout.
func ApplyPlan(ctx context.Context, c *cli.Client, plan *Plan, opts ApplyOpts) error {
	w := opts.Logger
	if w == nil {
		w = os.Stdout
	}
	for _, step := range plan.Steps {
		if err := applyStep(ctx, c, step, w); err != nil {
			return fmt.Errorf("step %s %s: %w", step.Action, stepTarget(step), err)
		}
	}
	return nil
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

// applyStep executes one plan step against the control-api and logs the
// outcome. Returns the control-api error, if any.
func applyStep(ctx context.Context, c *cli.Client, step Step, w io.Writer) error {
	logf := func(verb, target, status string) {
		fmt.Fprintf(w, "  %s %s %s\n", verb, target, status)
	}
	switch step.Action {
	case ActionRegister:
		if step.SpecBody == nil {
			return fmt.Errorf("register step missing spec body")
		}
		body := cli.RegisterTemplateRequest{Spec: *step.SpecBody, Source: step.Source}
		resp, err := c.RegisterTemplate(ctx, body)
		if err != nil {
			return err
		}
		logf("register", cli.TruncHash(resp.Hash()), "ok")
		return nil
	case ActionTagCreate:
		if _, err := c.CreateTag(ctx, cli.CreateTagRequest{Tag: step.Tag, Template: step.TemplateHash}); err != nil {
			// Conflict: tag already exists pointing at the same hash → ignore.
			if cli.IsConflict(err) {
				logf("tag", step.Tag, "skipped (already exists)")
				return nil
			}
			return err
		}
		logf("tag", step.Tag, "ok")
	case ActionTagMove:
		if _, err := c.MoveTag(ctx, step.Tag, cli.MoveTagRequest{Template: step.TemplateHash}); err != nil {
			return err
		}
		logf("tag-move", step.Tag, "ok")
	case ActionDeploy:
		ref := step.TemplateHash
		if ref == "" {
			ref = step.Tag
		}
		if _, err := c.DeployTemplate(ctx, ref); err != nil {
			return err
		}
		logf("deploy", ref, "ok")
	case ActionInstanceDelete:
		if err := c.DeleteInstance(ctx, step.InstanceID); err != nil {
			if cli.IsNotFound(err) {
				logf("instance-delete", step.InstanceKey, "skipped (already gone)")
				return nil
			}
			return err
		}
		logf("instance-delete", step.InstanceKey, "ok")
	case ActionUndeploy:
		if _, err := c.UndeployTemplate(ctx, step.TemplateHash); err != nil {
			if cli.IsConflict(err) {
				logf("undeploy", cli.TruncHash(step.TemplateHash), "skipped (still has active instances or already undeployed)")
				return nil
			}
			return err
		}
		logf("undeploy", cli.TruncHash(step.TemplateHash), "ok")
	case ActionTagDelete:
		if err := c.DeleteTag(ctx, step.Tag); err != nil {
			if cli.IsNotFound(err) {
				logf("tag-rm", step.Tag, "skipped (already gone)")
				return nil
			}
			return err
		}
		logf("tag-rm", step.Tag, "ok")
	case ActionInstanceCreate:
		key := step.InstanceKey
		body := cli.CreateInstanceRequest{
			Template:    step.TemplateTag,
			InstanceKey: &key,
			Params:      step.Params,
		}
		if _, err := c.CreateInstance(ctx, body); err != nil {
			return err
		}
		logf("create", step.InstanceKey, "ok")
	case ActionTemplateDelete:
		if err := c.DeleteTemplate(ctx, step.TemplateHash); err != nil {
			if cli.IsConflict(err) || cli.IsNotFound(err) {
				logf("template-delete", cli.TruncHash(step.TemplateHash), "skipped (still referenced)")
				return nil
			}
			return err
		}
		logf("template-delete", cli.TruncHash(step.TemplateHash), "ok")
	default:
		return fmt.Errorf("unknown action %s", step.Action)
	}
	return nil
}

// EmitPlan prints plan in human or JSON form per spec §3.2.
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

// destructive returns true if step needs --yes (or interactive
// confirmation) per spec §3.6.
//
// Most destructiveness is recorded at plan time on Step.Destructive
// (the bool is the authoritative signal — see plan.go where it's set).
// Undeploy steps are an exception: their destructiveness depends on
// whether non-compose instances are bound to the template, and that's
// computed against live state at apply time so a manifest change
// between plan and apply still bails. The undeploy-active-bindings
// table is computed once per apply (precomputeUndeployBindings) and
// passed in here, so destructive() is a pure check.
func destructive(step Step, undeployHasNonComposeBindings map[string]bool) bool {
	if step.Destructive {
		return true
	}
	if step.Action == ActionUndeploy && undeployHasNonComposeBindings[step.TemplateHash] {
		return true
	}
	return false
}

// precomputeUndeployBindings issues one ListInstances per unique
// undeploy hash and reports whether each has any non-compose-owned
// active binding. Unknown / fetch-error → marked destructive
// pessimistically (so the operator is forced to confirm).
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
		page, err := c.ListInstances(ctx, cli.ListInstancesQuery{TemplateHash: step.TemplateHash})
		if err != nil {
			out[step.TemplateHash] = true
			continue
		}
		for _, inst := range page.Instances {
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

// confirmDestructive prompts on a TTY or requires --yes. Returns true
// when the operator approved.
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

// composeUpFlags collects the parsed flags for `compose up`.
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
	cli.SetActiveCommonFlags(&out.common)
	return out, fs.Args(), 0
}

// loadManifestAndClient is the shared prelude for compose verbs.
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

// resolveTemplatePaths rewrites each template's Path to be absolute
// relative to the manifest's directory. Idempotent — already-absolute
// paths are left alone.
func resolveTemplatePaths(m *Manifest, manifestPath string) {
	mdir := filepath.Dir(manifestPath)
	for i := range m.Templates {
		if !filepath.IsAbs(m.Templates[i].Path) {
			m.Templates[i].Path = filepath.Join(mdir, m.Templates[i].Path)
		}
	}
}

// clientForManifest resolves the endpoint (manifest-pin precedence) and
// constructs the cli.Client. Returns (client, endpoint, exitCode).
func clientForManifest(flags *composeUpFlags, m *Manifest) (*cli.Client, string, int) {
	cfgPath, _ := cli.DefaultConfigPath()
	endpoint, err := cli.ResolveEndpointForCompose(flags.common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, m.Context)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, "", 2
	}
	return cli.NewClient(endpoint), endpoint, 0
}

// RunComposeUp implements `compose up`. Loads the manifest, resolves
// the client, and delegates to runComposeUpWithManifest.
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

// runComposeUpWithManifest is the shared body of `compose up`. Callers
// (RunComposeUp, RunDevUp) load the manifest once and thread it
// through, avoiding a second LoadManifest pass.
func runComposeUpWithManifest(ctx context.Context, m *Manifest, c *cli.Client, flags *composeUpFlags) int {
	state, err := QueryState(ctx, c, m.Project)
	if err != nil {
		return reportApplyError(err)
	}
	plan, err := ComputePlan(ctx, c, m, state)
	if err != nil {
		return reportPlanError(err)
	}
	// Destructive pre-check. Precompute the active-binding table for
	// undeploys once so we issue at most one ListInstances per unique
	// hash — not one per undeploy step (which previously drove N+1
	// calls on a plan with multiple undeploys).
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
	if err := ApplyPlan(ctx, c, plan, ApplyOpts{Logger: os.Stdout}); err != nil {
		return reportApplyError(err)
	}
	if plan.Summary.Changes == 0 {
		fmt.Fprintln(os.Stdout, "no changes")
	} else {
		fmt.Fprintf(os.Stdout, "applied %d changes\n", plan.Summary.Changes)
	}
	return 0
}

// RunComposePlan implements `compose plan`.
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
	// Exit 3 mirrors `terraform plan -detailed-exitcode`: any pending
	// change OR any drift warning (params drift on a non-terminal compose-
	// owned instance, where there is no step to schedule but the operator
	// still needs to know) must fail CI gating.
	if plan.Summary.Changes == 0 && !plan.HasDriftWarnings {
		return 0
	}
	return 3
}

// RunComposeStatus implements `compose status`.
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
	var apiErr *cli.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintln(os.Stderr, apiErr.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}
