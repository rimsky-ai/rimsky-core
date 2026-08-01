// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: lineage-record
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func RunLineagePrune(ctx context.Context, args []string) int {
	var before string
	var beforeDur time.Duration
	fs, common, endpoint, code := runWithCommon("lineage prune", args, func(fs *flag.FlagSet) {
		fs.StringVar(&before, "before", "", "RFC3339 timestamp; rows older than this are deleted")
		fs.DurationVar(&beforeDur, "older-than", 0, "shorthand: delete rows observed_at older than now-DURATION")
	})
	if code != 0 {
		return code
	}
	_ = fs
	if before == "" && beforeDur == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky lineage prune --before <RFC3339> | --older-than <duration>")
		return 2
	}
	if before == "" {
		before = time.Now().UTC().Add(-beforeDur).Format(time.RFC3339)
	}

	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	out, err := c.PruneLineage(ctx, before)
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, out)
		return 0
	}
	fmt.Fprintf(os.Stdout, "deleted %v lineage rows older than %s\n", out["deleted"], before)
	return 0
}
