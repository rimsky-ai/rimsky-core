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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/modeling/node"
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
	if err := fs.Parse(args); err != nil {
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
func readSpecFile(path string) (node.TemplateSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return node.TemplateSpec{}, err
	}
	var spec node.TemplateSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return spec, nil
}

// ReservedTagPrefix is the prefix that compose owns. CLI verbs that
// accept user-supplied tags reject this prefix to keep manual and
// compose namespaces disjoint.
const ReservedTagPrefix = "compose:"

// RunTemplateRegister implements `template register`.
func RunTemplateRegister(ctx context.Context, args []string) int {
	var tag, source string
	fs, common, endpoint, code := runWithCommon("template register", args, func(fs *flag.FlagSet) {
		fs.StringVar(&tag, "tag", "", "tag to attach to the registered template")
		fs.StringVar(&source, "source", "", "free-form source description")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli template register <file>")
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
	tpl, err := c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, Tag: tag, Source: source})
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, tpl)
	} else {
		EmitKV(os.Stdout, [][2]string{
			{"template_id", tpl.Hash()},
			{"tags", strings.Join(tpl.Tags, ",")},
		})
	}
	return 0
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
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli template get <ref>")
		return 2
	}
	c := NewClient(endpoint)
	tpl, err := c.GetTemplate(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, tpl)
		return 0
	}
	EmitKV(os.Stdout, [][2]string{
		{"id", tpl.Hash()},
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
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli template deploy <ref>")
		return 2
	}
	c := NewClient(endpoint)
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
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli template undeploy <ref>")
		return 2
	}
	c := NewClient(endpoint)
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
	fs, _, endpoint, code := runWithCommon("template rm", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli template rm <ref>")
		return 2
	}
	c := NewClient(endpoint)
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
