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
	if len(rows) == 0 {
		return report, nil
	}
	outbox := ProducerVerbOutboxOf(args)
	if outbox == nil {
		return HeldDurableReleaseReport{}, fmt.Errorf(
			"ReleaseHeldDurableClaims: no producer-verb outbox wired (RunArgs.VerbOutbox or a Tables backend providing one is required)")
	}
	if args.Clock == nil {
		return HeldDurableReleaseReport{}, fmt.Errorf("ReleaseHeldDurableClaims: RunArgs.Clock is required to stamp outbox rows")
	}
	now := args.Clock.Now()
	for _, r := range rows {
		producerName := ""
		if r.ProducerName != nil {
			producerName = *r.ProducerName
		}
		instID := instanceID
		if err := outbox.Enqueue(ctx, persistence.ProducerVerbOutboxInsertInput{
			ClaimHandleID:  r.ID,
			ProducerName:   producerName,
			Verb:           persistence.ProducerVerbRelease,
			ClaimScopeData: []byte(r.ClaimScopeData),
			Address:        []byte(r.Address),
			LeaseToken:     r.ProducerLeaseToken,
			SupervisorID:   args.SupervisorID,
			InstanceID:     &instID,
			NextAttemptAt:  now,
			EnqueuedAt:     now,
		}, tx); err != nil {
			report.Failures = append(report.Failures, HeldDurableReleaseFailure{
				ClaimHandleID: r.ID, ProducerName: producerName,
				Err: fmt.Errorf("enqueue release: %w", err),
			})
			if log != nil {
				log.Warn("ReleaseHeldDurableClaims: enqueue release failed; row preserved for retry",
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
