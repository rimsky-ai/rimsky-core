// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instance_termination.go — E9. Instance termination cleanup.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Held-durable claim lifecycle. When an instance terminates, the
// runtime walks held_durable claim_handles and calls
// `ClaimProducer.Release` on each (sequentially); failure to release
// does not block instance-termination completion — the operator can
// re-run the cleanup explicitly.
//
// @concept: held-durable
// @concept: claim-handle

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
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

// ReleaseHeldDurableClaims walks `held_durable=TRUE` claim_handles for
// the instance and calls `ClaimProducer.Release` on each. Returns a
// per-claim report so the operator can see which produced succeeded
// vs failed. The claim_handles row is deleted only on Release success;
// failures leave the row in place for retry.
//
// Caller responsibility: invoke inside an Instance.Terminate flow
// after all running runs have completed.
func ReleaseHeldDurableClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	instanceID shared.UUID, log shared.Logger,
) (HeldDurableReleaseReport, error) {
	rows, err := args.ClaimHandles.ListHeldDurableByInstance(ctx, instanceID, tx)
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
		if err := producer.Release(ctx, claimID, []byte(r.ScopeData), []byte(r.Address)); err != nil {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName, Err: err,
			})
			if log != nil {
				log.Warn("ReleaseHeldDurableClaims: producer.Release failed; row preserved for retry",
					"claim_handle_id", r.ID.String(), "producer", producerName, "err", err.Error())
			}
			continue
		}
		if err := args.ClaimHandles.Delete(ctx, r.ID, r.HolderSupervisorID, tx); err != nil {
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
