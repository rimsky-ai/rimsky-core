// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// output.go — human (table/text) and JSON formatters.
//
// ANSI color is on when stdout is a TTY and --no-color is not set.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// Format selects between human-readable and JSON outputs.
type Format int

const (
	// FormatHuman is the default for terminal use.
	FormatHuman Format = iota
	// @deliberate: FormatJSON emits raw JSON for scripting.
	FormatJSON
)

// ParseFormat parses a -o flag value.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "human", "text", "table":
		return FormatHuman, nil
	case "json":
		return FormatJSON, nil
	}
	return FormatHuman, fmt.Errorf("unknown output format %q (want human|json)", s)
}

// EmitJSON writes a value as indented JSON followed by a newline.
func EmitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// EmitTable writes a header row and data rows using tab alignment.
// Empty rows produce just the header.
//
// Honors the active --no-color flag (and NO_COLOR env, and stdout-not-
// a-tty heuristic) via ColorEnabled when w is os.Stdout/os.Stderr.
// When color is enabled, the header row is rendered in ANSI bold.
func EmitTable(w io.Writer, headers []string, rows [][]string) {
	noColor := activeNoColor()
	useColor := ColorEnabled(w, noColor)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if len(headers) > 0 {
		header := strings.Join(headers, "\t")
		if useColor {
			fmt.Fprintln(tw, ansiBold+header+ansiReset)
		} else {
			fmt.Fprintln(tw, header)
		}
	}
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// @constraint: AnsiGreen / AnsiRed are exported so package-external
// emitters (compose plan formatStep) can ask Colorize for matching
// color; ansiReset / ansiBold stay unexported because external callers
// reach them only through Colorize.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	AnsiGreen = "\x1b[32m"
	AnsiRed   = "\x1b[31m"
)

// activeNoColorFlag is the process-wide --no-color flag, set by
// SetActiveCommonFlags so output emitters can read it without
// threading CommonFlags through every callsite. Single-CLI-process
// scope; safe because the binary parses flags once and never mutates
// after.
var activeNoColorFlag bool

// SetActiveCommonFlags publishes the parsed common flags so emitters
// (EmitTable / colorized plan output) can honor --no-color without
// a parameter wired through every helper. Idempotent; safe to call
// from each verb.
func SetActiveCommonFlags(c *CommonFlags) {
	if c == nil {
		activeNoColorFlag = false
		return
	}
	activeNoColorFlag = c.NoColor
}

func activeNoColor() bool { return activeNoColorFlag }

// Colorize wraps s in an ANSI sequence when color is enabled on w.
// `code` is one of the ansi* constants above. Returns s unchanged when
// color is off so callers can use the result unconditionally.
func Colorize(w io.Writer, code, s string) string {
	if !ColorEnabled(w, activeNoColor()) {
		return s
	}
	return code + s + ansiReset
}

// EmitKV writes key:value lines.
func EmitKV(w io.Writer, pairs [][2]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	for _, p := range pairs {
		fmt.Fprintf(tw, "%s:\t%s\n", p[0], p[1])
	}
	_ = tw.Flush()
}

// ColorEnabled reports whether ANSI color codes should be emitted on w.
// True when w is a *os.File pointing at a TTY and noColorFlag is false.
func ColorEnabled(w io.Writer, noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
