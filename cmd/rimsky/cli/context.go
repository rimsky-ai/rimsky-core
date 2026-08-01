// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
)

func RunCtxList(args []string, configPath string) int {
	fs := flag.NewFlagSet("ctx list", flag.ContinueOnError)
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if err := common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	SetActiveCommonFlags(&common)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, redactedConfig(cfg))
		return 0
	}
	names := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		marker := ""
		if name == cfg.CurrentContext {
			marker = "*"
		}
		rows = append(rows, []string{marker, name, cfg.Contexts[name].Endpoint})
	}
	EmitTable(os.Stdout, []string{"", "NAME", "ENDPOINT"}, rows)
	return 0
}

const redactedAPIKeyPlaceholder = "REDACTED"

func redactedConfig(cfg *Config) *Config {
	out := &Config{CurrentContext: cfg.CurrentContext, Contexts: make(map[string]Context, len(cfg.Contexts))}
	for name, c := range cfg.Contexts {
		if c.APIKey != "" {
			c.APIKey = redactedAPIKeyPlaceholder
		}
		out.Contexts[name] = c
	}
	return out
}

func RunCtxUse(args []string, configPath string) int {
	fs := flag.NewFlagSet("ctx use", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky ctx use <name>")
		return 2
	}
	name := fs.Arg(0)
	if !ValidContextName(name) {
		fmt.Fprintf(os.Stderr, "invalid context name %q\n", name)
		return 2
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, ok := cfg.Contexts[name]; !ok {
		fmt.Fprintf(os.Stderr, "context %q not found; create it with `ctx add`\n", name)
		return 2
	}
	cfg.CurrentContext = name
	if err := SaveConfig(configPath, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "current context → %s (%s)\n", name, cfg.Contexts[name].Endpoint)
	return 0
}

func RunCtxAdd(args []string, configPath string) int {
	fs := flag.NewFlagSet("ctx add", flag.ContinueOnError)
	var endpoint string
	fs.StringVar(&endpoint, "endpoint", "", "API endpoint for the new context")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky ctx add <name> --endpoint <url>")
		return 2
	}
	name := fs.Arg(0)
	if !ValidContextName(name) {
		fmt.Fprintf(os.Stderr, "invalid context name %q\n", name)
		return 2
	}
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "--endpoint is required")
		return 2
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, ok := cfg.Contexts[name]; ok {
		fmt.Fprintf(os.Stderr, "context %q already exists\n", name)
		return 2
	}
	cfg.Contexts[name] = Context{Endpoint: endpoint}
	if cfg.CurrentContext == "" {
		cfg.CurrentContext = name
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "added context %s → %s\n", name, endpoint)
	return 0
}

func RunCtxRm(args []string, configPath string) int {
	fs := flag.NewFlagSet("ctx rm", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky ctx rm <name>")
		return 2
	}
	name := fs.Arg(0)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, ok := cfg.Contexts[name]; !ok {
		fmt.Fprintf(os.Stderr, "context %q not found\n", name)
		return 2
	}
	if name == cfg.CurrentContext {
		fmt.Fprintf(os.Stderr, "cannot remove current context %q; switch to another with `ctx use` first\n", name)
		return 2
	}
	delete(cfg.Contexts, name)
	if err := SaveConfig(configPath, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "removed context %s\n", name)
	return 0
}

func RunCtxCurrent(args []string, configPath string) int {
	fs := flag.NewFlagSet("ctx current", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if cfg.CurrentContext == "" {
		fmt.Fprintln(os.Stderr, "no current context set; run `ctx use <name>`")
		return 1
	}
	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		fmt.Fprintf(os.Stderr, "current context %q not defined in %s\n", cfg.CurrentContext, configPath)
		return 1
	}
	fmt.Fprintf(os.Stdout, "%s\t%s\n", cfg.CurrentContext, ctx.Endpoint)
	return 0
}
