// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// auth.go — `rimsky auth` dispatcher. Subcommands implement the CLI
// surface defined by spec
// .ok-planner/history/specs/2026-05-15-control-plane-mcp-and-auth-design.md
// "CLI / New subcommands".
package cli

import (
	"context"
	"fmt"
	"os"
)

// RunAuth dispatches `rimsky auth <subcommand> ...`.
func RunAuth(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky auth {init|login|create-key|list|show|revoke|rotate|status}")
		return 2
	}
	sub := args[0]
	rest := args[1:]
	ctx := context.Background()
	switch sub {
	case "init":
		return RunAuthInit(ctx, rest)
	case "login":
		return RunAuthLogin(ctx, rest)
	case "create-key":
		return RunAuthCreateKey(ctx, rest)
	case "list":
		return RunAuthList(ctx, rest)
	case "show":
		return RunAuthShow(ctx, rest)
	case "revoke":
		return RunAuthRevoke(ctx, rest)
	case "rotate":
		return RunAuthRotate(ctx, rest)
	case "status":
		return RunAuthStatus(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky auth {init|login|create-key|list|show|revoke|rotate|status}")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "unknown auth subcommand:", sub)
		return 2
	}
}
