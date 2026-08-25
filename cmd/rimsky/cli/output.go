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

	"gopkg.in/yaml.v3"
)

type Format int

const (
	FormatHuman Format = iota
	FormatJSON
	FormatYAML
	FormatTable
)

const FormatNames = "human|json|yaml|table"

func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "human":
		return FormatHuman, nil
	case "json":
		return FormatJSON, nil
	case "yaml":
		return FormatYAML, nil
	case "table":
		return FormatTable, nil
	}
	return FormatHuman, fmt.Errorf("unknown output format %q (want %s)", s, FormatNames)
}

func EmitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func EmitYAML(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var shaped any
	if err := json.Unmarshal(raw, &shaped); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "---\n"); err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(shaped); err != nil {
		return err
	}
	return enc.Close()
}

func (f Format) Structured() bool { return f == FormatJSON || f == FormatYAML }

func EmitStructured(w io.Writer, f Format, v any) error {
	if f == FormatYAML {
		return EmitYAML(w, v)
	}
	return EmitJSON(w, v)
}

type TableSupport bool

const (
	HasTable TableSupport = true
	NoTable  TableSupport = false
)

func Render(f Format, v any, human func()) int {
	if f.Structured() {
		_ = EmitStructured(os.Stdout, f, v)
		return 0
	}
	human()
	return 0
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

type removalResult struct {
	Ref      string `json:"ref"`
	Instance string `json:"instance,omitempty"`
	Removed  bool   `json:"removed"`
}

func reportRemoval(format Format, removed removalResult, humanLine string) int {
	return Render(format, removed, func() {
		fmt.Fprintln(os.Stdout, humanLine)
	})
}

type resetResult struct {
	Node  string `json:"node"`
	Reset bool   `json:"reset"`
}

func reportTagBinding(format Format, tag *Tag, name, template string) int {
	if tag == nil {
		tag = &Tag{Tag: name, TemplateID: template}
	}
	return Render(format, tag, func() {
		fmt.Fprintf(os.Stdout, "%s → %s\n", name, template)
	})
}
