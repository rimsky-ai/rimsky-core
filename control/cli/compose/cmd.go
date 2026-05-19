// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// cmd.go — sub-dispatchers for `compose <up|down|plan|status>` and
// `dev <up|down|status>`.
package compose

import (
	"context"
	"fmt"
	"os"
)

// Dispatch routes `compose <verb>` to the appropriate handler.
func Dispatch(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky compose <up|down|plan|status> ...")
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
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky compose <up|down|plan|status> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky compose: unknown subcommand %q\n", args[0])
	return 2
}

// DispatchDev routes `dev <verb>` to the appropriate handler.
func DispatchDev(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky dev <up|down|status> ...")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "up":
		return RunDevUp(ctx, rest)
	case "down":
		return RunDevDown(ctx, rest)
	case "status":
		return RunDevStatus(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky dev <up|down|status> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky dev: unknown subcommand %q\n", args[0])
	return 2
}
