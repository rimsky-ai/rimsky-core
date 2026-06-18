// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: claim-lifetime
// @concept: claim-handle

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type HeldDurableReleaseReport struct {
	Attempted int
	Succeeded int
	Failures  []HeldDurableReleaseFailure
}

type HeldDurableReleaseFailure struct {
	ClaimHandleID shared.UUID
	ProducerName  string
	Err           error
}

func ReleaseHeldDurableClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	instanceID shared.UUID, log shared.Logger,
) (HeldDurableReleaseReport, error) {
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
		producer, ok := args.StoreRegistry.GetWithContext(ctx, producerName, instanceID.String())
		if !ok {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName,
				Err: fmt.Errorf("unknown producer %q", producerName),
			})
			continue
		}
		claimID := claimproducer.ClaimID(r.ID.String())
		relCtx := peer.WithServiceName(ctx, producerName)
		if err := producer.Release(relCtx, claimID, []byte(r.ClaimScopeData), []byte(r.Address)); err != nil {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName, Err: err,
			})
			if log != nil {
				log.Warn("ReleaseHeldDurableClaims: producer.Release failed; row preserved for retry",
					"claim_handle_id", r.ID.String(), "producer", producerName, "err", err.Error())
			}
			continue
		}
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
