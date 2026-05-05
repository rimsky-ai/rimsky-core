// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main.go — rimsky-docs-lint. Six subcommands enforce structural integrity
// of the public-documentation surface (docs/concepts/, docs/protocols/,
// docs/agents/, docs/humans/, docs/glossary.md, docs/vocabulary.md).
package main

import (
	"fmt"
	"os"
)

type subcommand struct {
	name string
	fn   func(args []string) error
	desc string
}

var subcommands = []subcommand{
	{"frontmatter", runFrontmatter, "validate frontmatter shape on all concept files and error files"},
	{"glossary-parity", runGlossaryParity, "verify docs/glossary.md matches generator output"},
	{"vocabulary", runVocabulary, "scan public surface for forbidden terms"},
	{"citation-drift", runCitationDrift, "verify @source: citations match canonical definitions"},
	{"public-anchor-validity", runPublicAnchorValidity, "verify proto_symbol / config_field / api_surface anchors"},
	{"llms-txt-validity", runLLMSTxtValidity, "verify llms.txt is well-formed and links resolve"},
	{"all", runAll, "run all six lints; exits non-zero if any fail"},
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	for _, sc := range subcommands {
		if sc.name == cmd {
			if err := sc.fn(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", cmd)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rimsky-docs-lint <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "subcommands:")
	for _, sc := range subcommands {
		fmt.Fprintf(os.Stderr, "  %-25s %s\n", sc.name, sc.desc)
	}
}

// allLints is the static list of subcommand names runAll iterates. It mirrors
// the entries in `subcommands` (excluding "all" itself) but is a plain slice
// to avoid an initialization cycle between runAll and the subcommands table.
var allLints = []struct {
	name string
	fn   func(args []string) error
}{
	{"frontmatter", runFrontmatter},
	{"glossary-parity", runGlossaryParity},
	{"vocabulary", runVocabulary},
	{"citation-drift", runCitationDrift},
	{"public-anchor-validity", runPublicAnchorValidity},
	{"llms-txt-validity", runLLMSTxtValidity},
}

func runAll(args []string) error {
	var errs []error
	for _, sc := range allLints {
		if err := sc.fn(args); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sc.name, err))
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", sc.name, err)
		} else {
			fmt.Fprintf(os.Stderr, "OK   %s\n", sc.name)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d lint(s) failed", len(errs))
	}
	return nil
}
