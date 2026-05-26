// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instance_termination.go — E9. Instance termination cleanup.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Held-durable claim lifecycle. When an instance terminates, the
// runtime walks the instance's committed-durable claim_handles
// (state = 'committed' AND lifetime = 'durable') and calls
// `ClaimProducer.Release` on each (sequentially); failure to release
// does not block instance-termination completion — the operator can
// re-run the cleanup explicitly.
//
// @concept: claim-lifetime
// @concept: claim-handle

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
)

// HeldDurableReleaseReport summarizes the outcome of the
// instance-termination held-durable Release walk.
type HeldDurableReleaseReport struct {
	Attempted int
	Succeeded int
	Failures  []HeldDurableReleaseFailure
}

// HeldDurableReleaseFailure carries a per-claim Release failure for
// operator follow-up. Inert in rimsky.
type HeldDurableReleaseFailure struct {
	ClaimHandleID shared.UUID
	ProducerName  string
	Err           error
}

// ReleaseHeldDurableClaims walks the instance's committed-durable
// claim_handles (state = 'committed' AND lifetime = 'durable') and
// calls `ClaimProducer.Release` on each. Returns a per-claim report so
// the operator can see which producers succeeded vs failed. The
// claim_handles row is deleted only on Release success; failures leave
// the row in place for retry. The function name preserves the public
// surface from the pre-Stage-4 wire shape; internally the row-discovery
// query is `ListByInstanceAndState(committed, durable)`.
//
// Caller responsibility: invoke inside an Instance.Terminate flow
// after all running runs have completed.
func ReleaseHeldDurableClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	instanceID shared.UUID, log shared.Logger,
) (HeldDurableReleaseReport, error) {
	// Row-discovery via `ListByInstanceAndState(committed, durable)` —
	// the asset surface (state='committed' AND lifetime='durable').
	rows, err := args.ClaimHandles.ListByInstanceAndState(
		ctx, instanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable, tx,
	)
	if err != nil {
		return HeldDurableReleaseReport{}, fmt.Errorf("ReleaseHeldDurableClaims: list: %w", err)
	}
	report := HeldDurableReleaseReport{Attempted: len(rows)}
	for _, r := range rows {
		producerName := ""
		if r.ProducerName != nil {
			producerName = *r.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName,
				Err: fmt.Errorf("unknown producer %q", producerName),
			})
			continue
		}
		claimID := locks.ClaimID(r.ID.String())
		if err := producer.Release(ctx, claimID, []byte(r.ClaimScopeData), []byte(r.Address)); err != nil {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName, Err: err,
			})
			if log != nil {
				log.Warn("ReleaseHeldDurableClaims: producer.Release failed; row preserved for retry",
					"claim_handle_id", r.ID.String(), "producer", producerName, "err", err.Error())
			}
			continue
		}
		// DeleteResolved is absence-guarded — the row has
		// `holder_supervisor_id IS NULL` by construction post-Promote
		// (the post-Stage-4 CHECK constraint nulls the column whenever
		// `state` exits `'active'`). See @blessed-invariant 4 (post-
		// refactor): non-active-row deletions are guarded by absence +
		// the row-discovery query filter
		// (`ListByInstanceAndState(instance, committed, durable)`).
		if err := args.ClaimHandles.DeleteResolved(ctx, r.ID, tx); err != nil {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName,
				Err: fmt.Errorf("delete row: %w", err),
			})
			continue
		}
		report.Succeeded++
	}
	return report, nil
}
