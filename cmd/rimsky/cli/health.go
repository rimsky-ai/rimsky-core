// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func RunHealth(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if err := common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	SetActiveCommonFlags(&common)

	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	client := NewClient(endpoint)
	client.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	resp, err := client.Health(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	switch common.Format {
	case FormatJSON:
		_ = EmitJSON(os.Stdout, resp)
	default:
		EmitKV(os.Stdout, [][2]string{
			{"status", resp.Status},
			{"endpoint", endpoint},
			{"supervisors", fmt.Sprintf("%d", len(resp.Supervisors))},
		})
	}
	return 0
}
