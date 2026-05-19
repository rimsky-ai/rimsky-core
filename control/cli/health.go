// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// health.go — `rimsky health`. Prints the control-api's /health response.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// RunHealth implements the `health` verb.
func RunHealth(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	if err := fs.Parse(args); err != nil {
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
