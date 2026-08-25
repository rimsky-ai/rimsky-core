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
	var until string
	var olderThan time.Duration
	fs, common, endpoint, code := runWithCommon("lineage prune", "--until <RFC3339> | --older-than <duration>", NoTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&until, "until", "", "RFC3339 timestamp; rows older than this are deleted")
		fs.DurationVar(&olderThan, "older-than", 0, "shorthand: delete rows observed_at older than now-DURATION")
	})
	if common == nil {
		return code
	}
	if until == "" && olderThan == 0 {
		return UsageError(fs)
	}
	if until == "" {
		until = time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	}
	if !ConfirmDestructiveTargets(common.Yes, "prune lineage rows older than "+until) {
		return 2
	}

	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	out, err := c.PruneLineage(ctx, until)
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, out, func() {
		fmt.Fprintf(os.Stdout, "deleted %v lineage rows older than %s\n", out["deleted"], until)
	})
}
