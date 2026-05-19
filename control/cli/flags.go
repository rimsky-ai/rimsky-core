// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// flags.go — shared flag definitions for CLI subcommands.
package cli

import "flag"

// CommonFlags collects the flags every verb supports.
type CommonFlags struct {
	Endpoint string
	Key      string
	Format   Format
	Yes      bool
	NoColor  bool

	formatRaw string
}

// RegisterCommonFlags wires --endpoint, --key, -o, --yes, --no-color
// onto fs and stores parsed values into out. Call after fs construction
// and before fs.Parse. Resolve out.Format with out.ResolveFormat() after
// Parse — the raw string is captured during parse.
func RegisterCommonFlags(fs *flag.FlagSet, out *CommonFlags) {
	fs.StringVar(&out.Endpoint, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&out.Key, "key", "", "API key (Bearer token; or set RIMSKY_API_KEY)")
	fs.StringVar(&out.formatRaw, "o", "human", "output format: human|json")
	fs.StringVar(&out.formatRaw, "output", "human", "output format: human|json")
	fs.BoolVar(&out.Yes, "yes", false, "confirm destructive operations")
	fs.BoolVar(&out.NoColor, "no-color", false, "disable ANSI color")
}

// ResolveAPIKey returns the API key from --key, falling back to the
// RIMSKY_API_KEY env var. Empty string if neither is set (which is
// fine for anonymous-mode requests; the server returns 401 otherwise).
func (c *CommonFlags) ResolveAPIKey(envKey string) string {
	if c.Key != "" {
		return c.Key
	}
	return envKey
}

// ResolveFormat parses the captured -o value into Format. Call after Parse.
func (c *CommonFlags) ResolveFormat() error {
	f, err := ParseFormat(c.formatRaw)
	if err != nil {
		return err
	}
	c.Format = f
	return nil
}

// FormatRaw returns the raw `-o` / `--output` flag value the user
// passed (e.g. "human", "json"). Empty when the flag was not set.
// Useful for verbs that re-emit the flag onto a delegated subcommand
// (e.g. dev → compose).
func (c *CommonFlags) FormatRaw() string { return c.formatRaw }
