// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// flags.go — shared flag definitions for CLI subcommands.
package cli

import (
	"flag"
	"strings"
)

// parseInterspersed parses args into fs, tolerating flags that appear
// AFTER positional arguments. Stdlib flag.Parse stops interpreting flags
// at the first non-flag token, so `tag create my-tag --template ref`
// silently drops `--template` (the documented usage strings put the
// positional first, so the flag is never seen). This helper makes a
// single pre-pass that separates flags (and their values) from
// positionals, then hands fs.Parse the flags-first ordering it expects.
//
// Recognised forms: `--flag`, `-flag`, `--flag=value`, `-flag=value`, and
// `--flag value` (space-separated, for value-taking flags). A literal
// `--` terminates flag interpretation; everything after it is positional.
// A bare `-` is a positional. Unknown flags are passed through untouched
// so fs.Parse reports them exactly as before. Behaviour is identical to
// fs.Parse when flags already precede positionals.
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
		// @constraint: unknown flags must NOT consume the next token —
		// leaving them standalone lets fs.Parse surface the "not defined"
		// error instead of silently swallowing a positional.
		name := strings.TrimLeft(a, "-")
		if flagTakesValue(fs, name) && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return fs.Parse(append(flags, positionals...))
}

// flagTakesValue reports whether the named flag is registered on fs and
// is NOT a boolean flag (boolean flags accept `--flag` with no value).
// Unregistered flags return false: they don't consume the next token, so
// fs.Parse can report them unchanged.
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
