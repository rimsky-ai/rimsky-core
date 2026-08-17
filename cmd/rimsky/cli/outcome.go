// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

// @decision: exit-codes
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// @decision: exit-codes
const (
	ExitAllSuccess = 0
	ExitAnyFailure = 1
	ExitTimeout    = 2
	ExitInterrupt  = 130
)

// @concept: node-run
// @decision: exit-codes
func ClassifyInstanceOutcome(nodes []Node) (string, int) {
	for _, n := range nodes {
		if n.RunSummary != nil && n.RunSummary.FailedCount > 0 {
			return OutcomeFailure, len(nodes)
		}
	}
	return OutcomeSuccess, len(nodes)
}
