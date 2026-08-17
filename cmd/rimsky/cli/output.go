// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

type Format int

const (
	FormatHuman Format = iota
	FormatJSON
)

func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "human", "text", "table":
		return FormatHuman, nil
	case "json":
		return FormatJSON, nil
	}
	return FormatHuman, fmt.Errorf("unknown output format %q (want human|json)", s)
}

func EmitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

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

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	AnsiGreen = "\x1b[32m"
	AnsiRed   = "\x1b[31m"
)

var activeNoColorFlag bool

var activeFormatFlag Format

func SetActiveCommonFlags(c *CommonFlags) {
	if c == nil {
		activeNoColorFlag = false
		activeFormatFlag = FormatHuman
		return
	}
	activeNoColorFlag = c.NoColor
	activeFormatFlag = c.Format
}

func activeNoColor() bool { return activeNoColorFlag }

func Colorize(w io.Writer, code, s string) string {
	if !ColorEnabled(w, activeNoColor()) {
		return s
	}
	return code + s + ansiReset
}

func EmitKV(w io.Writer, pairs [][2]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	for _, p := range pairs {
		fmt.Fprintf(tw, "%s:\t%s\n", p[0], p[1])
	}
	_ = tw.Flush()
}

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
