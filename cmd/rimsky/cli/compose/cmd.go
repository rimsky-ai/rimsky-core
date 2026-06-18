// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"context"
	"fmt"
	"os"
)

func Dispatch(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky compose <up|down|plan|status|run> ...")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "up":
		return RunComposeUp(ctx, rest)
	case "down":
		return RunComposeDown(ctx, rest)
	case "plan":
		return RunComposePlan(ctx, rest)
	case "status":
		return RunComposeStatus(ctx, rest)
	case "run":
		return RunComposeRun(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky compose <up|down|plan|status|run> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky compose: unknown subcommand %q\n", args[0])
	return 2
}
