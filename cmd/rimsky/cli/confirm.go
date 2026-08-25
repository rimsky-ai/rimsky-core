// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// @concept: rimsky
func ConfirmDestructive(yes, interactive bool, in io.Reader, out io.Writer, targets []string) bool {
	if len(targets) == 0 || yes {
		return true
	}
	if !interactive {
		fmt.Fprintln(out, "destructive operations require --yes:")
		printConfirmTargets(out, targets)
		return false
	}
	fmt.Fprintln(out, "the following destructive operations are scheduled:")
	printConfirmTargets(out, targets)
	fmt.Fprint(out, "Proceed? [y/N] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}

func printConfirmTargets(out io.Writer, targets []string) {
	for _, t := range targets {
		fmt.Fprintln(out, "  "+t)
	}
}

// @concept: rimsky
func ConfirmDestructiveTargets(yes bool, targets ...string) bool {
	return ConfirmDestructive(yes, IsTerminal(os.Stdin), os.Stdin, os.Stderr, targets)
}

func IsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
