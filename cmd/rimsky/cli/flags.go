// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"flag"
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
	fs.StringVar(&out.Key, "key", "", "API key (Bearer token; or set RIMSKY_API_KEY)")
	fs.StringVar(&out.formatRaw, "o", "human", "output format: human|json")
	fs.StringVar(&out.formatRaw, "output", "human", "output format: human|json")
	fs.BoolVar(&out.Yes, "yes", false, "confirm destructive operations")
	fs.BoolVar(&out.NoColor, "no-color", false, "disable ANSI color")
}

// @concept: api-key
func (c *CommonFlags) ResolveAPIKey(envKey string) string {
	if c.Key != "" {
		return c.Key
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

func (c *CommonFlags) ResolveFormat() error {
	f, err := ParseFormat(c.formatRaw)
	if err != nil {
		return err
	}
	c.Format = f
	return nil
}

func (c *CommonFlags) FormatRaw() string { return c.formatRaw }
