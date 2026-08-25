// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if flagTakesValue(fs, name) && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return fs.Parse(append(flags, positionals...))
}

func flagTakesValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
		return false
	}
	return true
}

func UsageLine(name, argSpec string) string {
	line := "usage: rimsky " + name
	if argSpec != "" {
		line += " " + argSpec
	}
	return line
}

func SetUsage(fs *flag.FlagSet, line string) {
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, line)
		declared := 0
		fs.VisitAll(func(*flag.Flag) { declared++ })
		if declared == 0 {
			return
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
}

func ParseVerbFlags(fs *flag.FlagSet, args []string) (int, bool) {
	var captured bytes.Buffer
	fs.SetOutput(&captured)
	err := parseInterspersed(fs, args)
	fs.SetOutput(os.Stderr)
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		_, _ = os.Stdout.Write(captured.Bytes())
		return 0, true
	default:
		_, _ = os.Stderr.Write(captured.Bytes())
		return 2, true
	}
}

func UsageError(fs *flag.FlagSet) int {
	fs.SetOutput(os.Stderr)
	fs.Usage()
	return 2
}

type CommonFlags struct {
	Endpoint string
	Key      string
	Format   Format
	Yes      bool
	NoColor  bool

	formatRaw string
}

func RegisterCommonFlags(fs *flag.FlagSet, out *CommonFlags) {
	fs.StringVar(&out.Endpoint, "endpoint", "", "control-api endpoint URL")
	RegisterAPIKeyFlag(fs, &out.Key)
	RegisterOutputFlags(fs, out)
	RegisterYesFlag(fs, &out.Yes)
}

func RegisterYesFlag(fs *flag.FlagSet, out *bool) {
	fs.BoolVar(out, "yes", false, "confirm destructive operations")
	// @decision: short-flags-single-letter
	fs.BoolVar(out, "y", false, "short for --yes")
}

func RegisterOutputFlags(fs *flag.FlagSet, out *CommonFlags) {
	fs.StringVar(&out.formatRaw, "output", "human", "output format: "+FormatNames)
	// @decision: short-flags-single-letter
	fs.StringVar(&out.formatRaw, "o", "human", "short for --output")
	fs.BoolVar(&out.NoColor, "no-color", false, "disable ANSI color")
}

// @concept: rimsky
func RegisterAPIKeyFlag(fs *flag.FlagSet, out *string) {
	fs.StringVar(out, "key", "", "API key (Bearer token; or set RIMSKY_API_KEY)")
}

// @concept: api-key
// @concept: rimsky
func ResolveAPIKey(flagValue, envKey string) string {
	if flagValue != "" {
		return flagValue
	}
	if envKey != "" {
		return envKey
	}
	cfgPath, err := DefaultConfigPath()
	if err != nil {
		return ""
	}
	return ResolveAPIKeyFromContext(cfgPath)
}

func (c *CommonFlags) ResolveAPIKey(envKey string) string {
	return ResolveAPIKey(c.Key, envKey)
}

func (c *CommonFlags) ResolveFormat(verb string, tables TableSupport) error {
	f, err := ParseFormat(c.formatRaw)
	if err != nil {
		return err
	}
	if f == FormatTable && tables == NoTable {
		return fmt.Errorf("rimsky %s: -o table names a rendering this verb does not have; use human, json, or yaml", verb)
	}
	c.Format = f
	return nil
}

func (c *CommonFlags) FormatRaw() string { return c.formatRaw }
