// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
)

func runWithCommon(name, argSpec string, tables TableSupport, args []string, registerExtra func(fs *flag.FlagSet)) (*flag.FlagSet, *CommonFlags, string, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	SetUsage(fs, UsageLine(name, argSpec))
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	if registerExtra != nil {
		registerExtra(fs)
	}
	if code, done := ParseVerbFlags(fs, args); done {
		return nil, nil, "", code
	}
	if err := common.ResolveFormat(name, tables); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", 2
	}
	SetActiveCommonFlags(&common)
	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API_URL"), cfgPath, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", 2
	}
	return fs, &common, endpoint, 0
}

func reportError(err error) int {
	// @decision: auth-dry-run-request-flag
	if code, ok := ReportDryRunPreview(err); ok {
		return code
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintln(os.Stderr, apiErr.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// @concept: rimsky
func ReadSpecFile(path string) (node.TemplateSpec, error) {
	if _, err := os.Stat(path); err != nil {
		return node.TemplateSpec{}, err
	}
	var generic any
	if err := configload.LoadFile(path, &generic); err != nil {
		return node.TemplateSpec{}, err
	}
	baseDir := filepath.Dir(path)
	resolved, err := resolveSourceFileRefs(generic, baseDir)
	if err != nil {
		return node.TemplateSpec{}, fmt.Errorf("resolve source_file in %s: %w", path, err)
	}
	resolvedBytes, err := yaml.Marshal(resolved)
	if err != nil {
		return node.TemplateSpec{}, fmt.Errorf("marshal resolved %s: %w", path, err)
	}
	var spec node.TemplateSpec
	if err := configload.DecodeStrict(path, resolvedBytes, &spec); err != nil {
		return node.TemplateSpec{}, err
	}
	return spec, nil
}

func resolveSourceFileRefs(node any, baseDir string) (any, error) {
	switch v := node.(type) {
	case map[string]any:
		if len(v) == 1 {
			if raw, ok := v["source_file"]; ok {
				if pathStr, isString := raw.(string); isString {
					return readSourceFile(pathStr, baseDir)
				}
			}
		}
		out := make(map[string]any, len(v))
		for k, child := range v {
			resolved, err := resolveSourceFileRefs(child, baseDir)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			resolved, err := resolveSourceFileRefs(child, baseDir)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return v, nil
	}
}

func readSourceFile(inputPath, baseDir string) (string, error) {
	if inputPath == "" {
		return "", fmt.Errorf("source_file: path is empty")
	}
	if filepath.IsAbs(inputPath) {
		return "", fmt.Errorf("source_file: %q is absolute; only template-relative paths are allowed", inputPath)
	}
	cleaned := filepath.Clean(filepath.Join(baseDir, inputPath))
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("source_file: resolve base dir: %w", err)
	}
	absCleaned, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("source_file: resolve %q: %w", inputPath, err)
	}
	rel, err := filepath.Rel(absBase, absCleaned)
	if err != nil {
		return "", fmt.Errorf("source_file: %q escapes the template directory: %w", inputPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source_file: %q escapes the template directory", inputPath)
	}
	data, err := os.ReadFile(cleaned)
	if err != nil {
		return "", fmt.Errorf("source_file: read %q: %w", inputPath, err)
	}
	return string(data), nil
}

func RunTemplateRegister(ctx context.Context, args []string) int {
	return runTemplateRegisterNamed(ctx, "template register", args)
}

func runTemplateRegisterNamed(ctx context.Context, name string, args []string) int {
	var tag, source string
	var warningsAsErrors bool
	fs, common, endpoint, code := runWithCommon(name, "<file> [--warnings-as-errors]", NoTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&tag, "tag", "", "tag to attach to the registered template")
		fs.StringVar(&source, "source", "", "free-form source description")
		fs.BoolVar(&warningsAsErrors, "warnings-as-errors", false,
			"reject registration if the validation pipeline produced any warnings")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	spec, err := ReadSpecFile(rest[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.RegisterTemplateWithOptions(ctx,
		RegisterTemplateRequest{Spec: spec, Tag: tag, Source: source},
		RegisterTemplateOptions{WarningsAsErrors: warningsAsErrors},
	)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 400 && apiErr.Body != nil {
			if w, ok := apiErr.Body["validation_warnings"]; ok {
				fmt.Fprintln(os.Stderr, "validation warnings:")
				_ = EmitJSON(os.Stderr, w)
			}
			if e, ok := apiErr.Body["validation_errors"]; ok {
				fmt.Fprintln(os.Stderr, "validation errors:")
				_ = EmitJSON(os.Stderr, e)
			}
		}
		return reportError(err)
	}
	// @story: validation-warnings-surfaced
	return Render(common.Format, tpl, func() {
		for _, w := range tpl.ValidationWarnings {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", w.Path, w.Msg)
		}
		EmitKV(os.Stdout, [][2]string{
			{"template_hash", tpl.Hash()},
			{"tags", strings.Join(tpl.Tags, ",")},
		})
	})
}

func RunTemplateLint(ctx context.Context, args []string) int {
	var warningsAsErrors bool
	fs, common, endpoint, code := runWithCommon("template lint", "[--warnings-as-errors] <file>", NoTable, args, func(fs *flag.FlagSet) {
		fs.BoolVar(&warningsAsErrors, "warnings-as-errors", false,
			"treat validation warnings as findings (exit 1 if any warnings)")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	spec, err := ReadSpecFile(rest[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	res, err := c.ValidateTemplate(ctx, RegisterTemplateRequest{Spec: spec}, warningsAsErrors)
	if err != nil {
		return reportError(err)
	}

	Render(common.Format, res, func() {
		for _, w := range res.ValidationWarnings {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", w.Path, w.Msg)
		}
		for _, e := range res.ValidationErrors {
			fmt.Fprintf(os.Stderr, "error: %s: %s\n", e.Path, e.Msg)
		}
		if res.Ok {
			fmt.Fprintln(os.Stdout, "ok")
		}
	})
	if res.Ok {
		return 0
	}
	return 1
}

func RunTemplateList(ctx context.Context, args []string) int {
	return runTemplateListNamed(ctx, "template list", args)
}

func runTemplateListNamed(ctx context.Context, name string, args []string) int {
	var stateFlag, tagPrefix string
	_, common, endpoint, code := runWithCommon(name, "[--state <state>] [--tag-prefix <prefix>]", HasTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&stateFlag, "state", "", "filter by state")
		fs.StringVar(&tagPrefix, "tag-prefix", "", "client-side filter on attached tag prefix")
	})
	if common == nil {
		return code
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	q := ListTemplatesQuery{State: stateFlag}
	all, err := pagedListTemplates(ctx, c, q)
	if err != nil {
		return reportError(err)
	}
	if tagPrefix != "" {
		filtered := all[:0]
		for _, t := range all {
			for _, tg := range t.Tags {
				if strings.HasPrefix(tg, tagPrefix) {
					filtered = append(filtered, t)
					break
				}
			}
		}
		all = filtered
	}
	sort.Slice(all, func(i, j int) bool {
		return primaryTag(all[i].Tags) < primaryTag(all[j].Tags)
	})
	return Render(common.Format, all, func() {
		rows := make([][]string, 0, len(all))
		for _, t := range all {
			rows = append(rows, []string{
				TruncHash(t.Hash()),
				t.State,
				strings.Join(t.Tags, ","),
			})
		}
		EmitTable(os.Stdout, []string{"HASH", "STATE", "TAGS"}, rows)
	})
}

func primaryTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out := append([]string(nil), tags...)
	sort.Strings(out)
	return out[0]
}

func pagedListTemplates(ctx context.Context, c *Client, q ListTemplatesQuery) ([]Template, error) {
	return PageAll(func(cursor string) ([]Template, string, error) {
		q.Cursor = cursor
		page, err := c.ListTemplates(ctx, q)
		if err != nil {
			return nil, "", err
		}
		return page.Templates, page.NextCursor, nil
	})
}

func RunTemplateGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("template get", "<ref>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.GetTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, tpl, func() {
		EmitKV(os.Stdout, [][2]string{
			{"template_hash", tpl.Hash()},
			{"state", tpl.State},
			{"tags", strings.Join(tpl.Tags, ",")},
			{"source", tpl.Source},
		})
	})
}

func RunTemplateDeploy(ctx context.Context, args []string) int {
	return runTemplateDeployNamed(ctx, "template deploy", args)
}

func runTemplateDeployNamed(ctx context.Context, name string, args []string) int {
	fs, common, endpoint, code := runWithCommon(name, "<ref>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.DeployTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, tpl, func() {
		if tpl.NoOp {
			fmt.Fprintf(os.Stdout, "%s already deployed\n", rest[0])
		} else {
			fmt.Fprintf(os.Stdout, "%s deployed\n", rest[0])
		}
	})
}

func RunTemplateUndeploy(ctx context.Context, args []string) int {
	return runTemplateUndeployNamed(ctx, "template undeploy", args)
}

func runTemplateUndeployNamed(ctx context.Context, name string, args []string) int {
	fs, common, endpoint, code := runWithCommon(name, "<ref>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if !ConfirmDestructiveTargets(common.Yes, "undeploy template "+rest[0]) {
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.UndeployTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, tpl, func() {
		if tpl.NoOp {
			fmt.Fprintf(os.Stdout, "%s already undeployed\n", rest[0])
		} else {
			fmt.Fprintf(os.Stdout, "%s undeployed\n", rest[0])
		}
	})
}

func RunTemplateRm(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("template rm", "<ref>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if !ConfirmDestructiveTargets(common.Yes, "remove template "+rest[0]) {
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.DeleteTemplate(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	return reportRemoval(common.Format, removalResult{Ref: rest[0], Removed: true},
		fmt.Sprintf("%s removed", rest[0]))
}

func JSONString(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
