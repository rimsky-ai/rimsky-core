// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// templates.go — `template register/list/get/deploy/undeploy/rm`.
//
// All handlers share the same boilerplate: own a flag set, register
// CommonFlags, parse args, resolve endpoint, call the typed Client,
// emit human or JSON output. Exit codes follow spec §5.3:
// 0 success, 1 runtime/control-api error, 2 usage/local validation.
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
)

// runWithCommon is a small helper that initializes a flag set, registers
// the common flags, parses args, resolves the format, and resolves the
// endpoint. Returns the resolved endpoint and parsed positionals; on
// error returns a non-zero exit code via *exitCode.
func runWithCommon(name string, args []string, registerExtra func(fs *flag.FlagSet)) (*flag.FlagSet, *CommonFlags, string, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	if registerExtra != nil {
		registerExtra(fs)
	}
	if err := parseInterspersed(fs, args); err != nil {
		return nil, nil, "", 2
	}
	if err := common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", 2
	}
	SetActiveCommonFlags(&common)
	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", 2
	}
	return fs, &common, endpoint, 0
}

// reportError prints err and returns 1 unless err is an APIError with a
// known classification, in which case it formats accordingly.
func reportError(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintln(os.Stderr, apiErr.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// readSpecFile reads <path> as YAML and decodes it into node.TemplateSpec.
// The control-api accepts JSON-shaped bodies; YAML is the on-disk form.
// yaml.v3 honors the json: tags' lowercase-snake-case keys via its own
// yaml: tags (already declared on the spec types).
//
// Before typed-spec decode, the YAML tree is walked once and every
// occurrence of `{source_file: <relative-path>}` is replaced with the
// referenced file's text content. Resolution is single-pass: inlined
// content is not re-scanned for further `source_file:` references.
// Path resolution is relative to the spec file's directory; absolute
// paths and paths that escape that directory are rejected with
// exit-code-2 grade errors. Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 2.
//
// @concept: rimsky (CLI-side source_file: resolution)
func readSpecFile(path string) (node.TemplateSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return node.TemplateSpec{}, err
	}
	// Decode to generic structure first so we can resolve source_file:
	// references before typed-spec decode.
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
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
	if err := yaml.Unmarshal(resolvedBytes, &spec); err != nil {
		return node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return spec, nil
}

// resolveSourceFileRefs walks a yaml.Unmarshal-shaped tree (typed as
// `any`, possibly containing `map[string]any` or `[]any`) and replaces
// every object of the exact shape `{source_file: "<path>"}` with the
// referenced file's text content as a plain `string`. Path resolution
// is relative to baseDir; resolved paths that escape baseDir (via `..`)
// or that are absolute are rejected. Returns the transformed tree.
//
// Single-pass discipline: a file's contents are inlined as plain text
// and are NOT re-walked for further `source_file:` references; this
// avoids indirection chains and cycle-detection complexity.
//
// The "exact shape" rule: only an object with exactly one entry whose
// key is `source_file` and whose value is a string qualifies. Objects
// with additional siblings or non-string `source_file` values are
// left intact (they may be legitimate attribute fragments — e.g. an
// attribute `default:` whose value happens to mention `source_file:`
// inside other keys).
//
// Implementation note: yaml.v3 unmarshals YAML mappings into
// `map[string]any` and sequences into `[]any`, so those are the only
// container shapes we walk.
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
		// scalars (string, int, float, bool, nil) pass through unchanged.
		return v, nil
	}
}

// readSourceFile resolves `inputPath` relative to baseDir, rejects
// absolute paths and paths that escape baseDir, and returns the file's
// text content as a string. Failures here surface to the CLI as
// exit-code-2 (usage / local-validation) errors via readSpecFile's
// wrapper.
func readSourceFile(inputPath, baseDir string) (string, error) {
	if inputPath == "" {
		return "", fmt.Errorf("source_file: path is empty")
	}
	if filepath.IsAbs(inputPath) {
		return "", fmt.Errorf("source_file: %q is absolute; only template-relative paths are allowed", inputPath)
	}
	cleaned := filepath.Clean(filepath.Join(baseDir, inputPath))
	// Use absolute baseDir for the containment check so that both sides
	// of filepath.Rel are anchored the same way regardless of the
	// caller's cwd.
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

// ReservedTagPrefix is the prefix that compose owns. CLI verbs that
// accept user-supplied tags reject this prefix to keep manual and
// compose namespaces disjoint.
const ReservedTagPrefix = "compose:"

// RunTemplateRegister implements `template register`.
//
// G6 adds the `--warnings-as-errors` flag. When set, the CLI forwards
// `?warnings_as_errors=true` to the control-API; the server rejects
// the registration if the validation pipeline produced any warnings
// (in addition to errors). The CLI surfaces the body's
// `validation_warnings` array on stderr when the rejection is
// warning-driven so the operator sees what was escalated.
func RunTemplateRegister(ctx context.Context, args []string) int {
	var tag, source string
	var warningsAsErrors bool
	fs, common, endpoint, code := runWithCommon("template register", args, func(fs *flag.FlagSet) {
		fs.StringVar(&tag, "tag", "", "tag to attach to the registered template")
		fs.StringVar(&source, "source", "", "free-form source description")
		fs.BoolVar(&warningsAsErrors, "warnings-as-errors", false,
			"reject registration if the validation pipeline produced any warnings")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template register <file> [--warnings-as-errors]")
		return 2
	}
	if strings.HasPrefix(tag, ReservedTagPrefix) {
		fmt.Fprintf(os.Stderr, "tag %q uses reserved prefix %q (managed by `compose`)\n", tag, ReservedTagPrefix)
		return 2
	}
	spec, err := readSpecFile(rest[0])
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
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, tpl)
	} else {
		EmitKV(os.Stdout, [][2]string{
			{"template_hash", tpl.Hash()},
			{"tags", strings.Join(tpl.Tags, ",")},
		})
	}
	return 0
}

// RunTemplateLint implements `template lint`: validate a spec file
// against the control-api without persisting. Exit codes diverge from
// the rest of the template verbs by linter convention: 0 = clean, 1 =
// findings (or a transport/control-api error), 2 = usage/local error.
// The non-zero-on-findings rule deliberately extends the general
// "1 = runtime error" convention so the verb composes in CI scripts.
// Per spec 2026-05-28-quality-of-life-features.
func RunTemplateLint(ctx context.Context, args []string) int {
	var warningsAsErrors bool
	fs, common, endpoint, code := runWithCommon("template lint", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&warningsAsErrors, "warnings-as-errors", false,
			"treat validation warnings as findings (exit 1 if any warnings)")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template lint [--warnings-as-errors] <file>")
		return 2
	}
	spec, err := readSpecFile(rest[0])
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

	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, res)
	} else {
		for _, w := range res.ValidationWarnings {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", w.Path, w.Msg)
		}
		for _, e := range res.ValidationErrors {
			fmt.Fprintf(os.Stderr, "error: %s: %s\n", e.Path, e.Msg)
		}
		if res.Ok {
			fmt.Fprintln(os.Stdout, "ok")
		}
	}
	if res.Ok {
		return 0
	}
	return 1
}

// RunTemplateList implements `template list`.
func RunTemplateList(ctx context.Context, args []string) int {
	var stateFlag, tagPrefix string
	fs, common, endpoint, code := runWithCommon("template list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&stateFlag, "state", "", "filter by state")
		fs.StringVar(&tagPrefix, "tag-prefix", "", "client-side filter on attached tag prefix")
	})
	if code != 0 {
		return code
	}
	_ = fs
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
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, all)
		return 0
	}
	rows := make([][]string, 0, len(all))
	for _, t := range all {
		rows = append(rows, []string{
			TruncHash(t.Hash()),
			t.State,
			strings.Join(t.Tags, ","),
		})
	}
	EmitTable(os.Stdout, []string{"HASH", "STATE", "TAGS"}, rows)
	return 0
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
	var all []Template
	for {
		page, err := c.ListTemplates(ctx, q)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Templates...)
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	return all, nil
}

// RunTemplateGet implements `template get`.
func RunTemplateGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("template get", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template get <ref>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.GetTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, tpl)
		return 0
	}
	EmitKV(os.Stdout, [][2]string{
		{"template_hash", tpl.Hash()},
		{"state", tpl.State},
		{"tags", strings.Join(tpl.Tags, ",")},
		{"source", tpl.Source},
	})
	return 0
}

// RunTemplateDeploy implements `template deploy`.
func RunTemplateDeploy(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("template deploy", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template deploy <ref>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.DeployTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, tpl)
		return 0
	}
	if tpl.NoOp {
		fmt.Fprintf(os.Stdout, "%s already deployed\n", rest[0])
	} else {
		fmt.Fprintf(os.Stdout, "%s deployed\n", rest[0])
	}
	return 0
}

// RunTemplateUndeploy implements `template undeploy`.
func RunTemplateUndeploy(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("template undeploy", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template undeploy <ref>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	tpl, err := c.UndeployTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, tpl)
		return 0
	}
	if tpl.NoOp {
		fmt.Fprintf(os.Stdout, "%s already undeployed\n", rest[0])
	} else {
		fmt.Fprintf(os.Stdout, "%s undeployed\n", rest[0])
	}
	return 0
}

// RunTemplateRm implements `template rm`.
func RunTemplateRm(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("template rm", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template rm <ref>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.DeleteTemplate(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "%s removed\n", rest[0])
	return 0
}

// JSONString round-trips v through json so tests can compare output.
func JSONString(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
