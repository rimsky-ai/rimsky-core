// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"fmt"
	"os"
)

func RunHealth(ctx context.Context, args []string) int {
	_, common, endpoint, code := runWithCommon("health", args, nil)
	if code != 0 {
		return code
	}

	client := NewClient(endpoint)
	client.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	resp, err := client.Health(ctx)
	if err != nil {
		return reportError(err)
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
