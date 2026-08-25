// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"fmt"
	"os"
)

func RunHealth(ctx context.Context, args []string) int {
	_, common, endpoint, code := runWithCommon("health", "", NoTable, args, nil)
	if common == nil {
		return code
	}

	client := NewClient(endpoint)
	client.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	resp, err := client.Health(ctx)
	if err != nil {
		return reportError(err)
	}

	return Render(common.Format, resp, func() {
		EmitKV(os.Stdout, [][2]string{
			{"status", resp.Status},
			{"endpoint", endpoint},
			{"supervisors", fmt.Sprintf("%d", len(resp.Supervisors))},
		})
	})
}
