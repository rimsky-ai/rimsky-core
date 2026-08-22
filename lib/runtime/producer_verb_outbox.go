// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: terminal-resolution
// @concept: claim-producer

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const (
	defaultProducerVerbPollInterval = 5 * time.Second
	defaultProducerVerbBaseBackoff  = 1 * time.Second
	defaultProducerVerbMaxBackoff   = 60 * time.Second
)

type producerVerbOutboxProvider interface {
	ProducerVerbOutbox() persistence.ProducerVerbOutboxTable
}

func ProducerVerbOutboxOf(args RunArgs) persistence.ProducerVerbOutboxTable {
	if args.VerbOutbox != nil {
		return args.VerbOutbox
	}
	if p, ok := args.Persist.(producerVerbOutboxProvider); ok {
		return p.ProducerVerbOutbox()
	}
	return nil
}

func producerVerbForOutcome(o TerminalOutcome) (persistence.ProducerVerb, error) {
	if o == OutcomeCommit {
		return persistence.ProducerVerbCommit, nil
	}
	if o.IsAbandon() {
		return persistence.ProducerVerbAbandon, nil
	}
	return "", fmt.Errorf("producerVerbForOutcome: unknown outcome %v", o)
}

// @decision: promotion-lineage-record-after-commit
func enqueueProducerVerb(
	ctx context.Context, args RunArgs, td TerminalDecision, pendingLineage []byte, tx persistence.Tx,
) error {
	outbox := ProducerVerbOutboxOf(args)
	if outbox == nil {
		return nil
	}
	if args.Clock == nil {
		return fmt.Errorf("enqueueProducerVerb: RunArgs.Clock is required to stamp outbox rows (claim_handle %s)", td.ClaimHandleID)
	}
	verb, err := producerVerbForOutcome(td.Outcome)
	if err != nil {
		return err
	}
	producerName := td.ProducerName
	if producerName == "" && td.Producer != nil {
		producerName = td.Producer.Name()
	}
	var instanceID *shared.UUID
	if td.LineageHint.InstanceID != (shared.UUID{}) {
		id := td.LineageHint.InstanceID
		instanceID = &id
	}
	now := args.Clock.Now()
	if err := outbox.Enqueue(ctx, persistence.ProducerVerbOutboxInsertInput{
		ClaimHandleID:        td.ClaimHandleID,
		ProducerName:         producerName,
		Verb:                 verb,
		ClaimScopeData:       td.Scope,
		Address:              td.Address,
		LeaseToken:           td.LeaseToken,
		SupervisorID:         td.SupervisorID,
		InstanceID:           instanceID,
		ParentClaimHandleID:  td.ParentClaimHandleID,
		NextAttemptAt:        now,
		EnqueuedAt:           now,
		PendingLineageRecord: pendingLineage,
	}, tx); err != nil {
		return fmt.Errorf("enqueueProducerVerb(%s, %s): %w", producerName, verb, err)
	}
	return nil
}

func kickProducerVerbDispatch(args RunArgs) postCommitFn {
	if args.ProducerVerbKick == nil {
		return nil
	}
	kick := args.ProducerVerbKick
	return func(context.Context) { kick() }
}

func FlushProducerVerbOutbox(ctx context.Context, args RunArgs) (int, error) {
	outbox := ProducerVerbOutboxOf(args)
	if outbox == nil {
		return 0, nil
	}
	clock := args.Clock
	if clock == nil {
		clock = shared.SystemClock{}
	}
	d := NewProducerVerbDispatcher(outbox, args.Persist, args.ClaimProducerRegistry, clock, args.Logger)
	return d.DispatchOnce(ctx)
}

type ProducerVerbResolver interface {
	ResolveWithContext(ctx context.Context, name string, instanceID string, tx persistence.Tx) (claimproducer.ClaimProducer, bool, error)
}

type ProducerVerbDispatcher struct {
	Outbox    persistence.ProducerVerbOutboxTable
	Tables    persistence.Tables
	Producers ProducerVerbResolver
	Clock     shared.Clock
	Logger    shared.Logger

	PollInterval time.Duration
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration

	kick chan struct{}
}

func NewProducerVerbDispatcher(
	outbox persistence.ProducerVerbOutboxTable,
	tables persistence.Tables,
	producers ProducerVerbResolver,
	clock shared.Clock,
	logger shared.Logger,
) *ProducerVerbDispatcher {
	return &ProducerVerbDispatcher{
		Outbox:       outbox,
		Tables:       tables,
		Producers:    producers,
		Clock:        clock,
		Logger:       logger,
		PollInterval: defaultProducerVerbPollInterval,
		BaseBackoff:  defaultProducerVerbBaseBackoff,
		MaxBackoff:   defaultProducerVerbMaxBackoff,
		kick:         make(chan struct{}, 1),
	}
}

func (d *ProducerVerbDispatcher) Kick() {
	if d == nil || d.kick == nil {
		return
	}
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

func (d *ProducerVerbDispatcher) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := d.DispatchOnce(ctx); err != nil && d.Logger != nil {
			d.Logger.Warn("producer_verb_outbox.dispatch_pass_failed", "error", err.Error())
		}
		interval := d.PollInterval
		if interval <= 0 {
			interval = defaultProducerVerbPollInterval
		}
		sleepDone := make(chan struct{})
		go func() {
			_ = d.Clock.Sleep(ctx, interval)
			close(sleepDone)
		}()
		select {
		case <-ctx.Done():
			return
		case <-d.kick:
		case <-sleepDone:
		}
	}
}

func producerVerbScopeKey(producerName string, scope []byte) string {
	return producerName + "\x00" + string(scope)
}

func producerVerbBackoff(attemptCount int, base, max time.Duration) time.Duration {
	if base <= 0 {
		base = defaultProducerVerbBaseBackoff
	}
	if max <= 0 {
		max = defaultProducerVerbMaxBackoff
	}
	backoff := base
	for i := 1; i < attemptCount; i++ {
		backoff *= 2
		if backoff >= max {
			return max
		}
	}
	if backoff > max {
		return max
	}
	return backoff
}

// @concept: terminal-resolution
func (d *ProducerVerbDispatcher) DispatchOnce(ctx context.Context) (int, error) {
	rows, err := d.Outbox.ListAll(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("ProducerVerbDispatcher: ListAll: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	delivered := 0
	blocked := make(map[string]bool)
	now := d.Clock.Now()
	for _, row := range rows {
		key := producerVerbScopeKey(row.ProducerName, row.ClaimScopeData)
		if blocked[key] {
			continue
		}
		if row.NextAttemptAt.After(now) {
			blocked[key] = true
			continue
		}
		if err := d.deliverRowAndClear(ctx, row); err != nil {
			blocked[key] = true
			backoff := producerVerbBackoff(row.AttemptCount+1, d.BaseBackoff, d.MaxBackoff)
			if rerr := d.Outbox.RecordAttempt(ctx, row.Seq, now.Add(backoff), err.Error(), nil); rerr != nil && d.Logger != nil {
				d.Logger.Warn("producer_verb_outbox.record_attempt_failed",
					"seq", row.Seq,
					"producer", row.ProducerName,
					"error", rerr.Error(),
					"consequence", "the row keeps its previous next-attempt time, so the next pass retries it earlier than the backoff asks")
			}
			if d.Logger != nil {
				d.Logger.Warn("producer_verb_outbox.delivery_retry_scheduled",
					"seq", row.Seq,
					"producer", row.ProducerName,
					"verb", string(row.Verb),
					"claim_handle_id", row.ClaimHandleID.String(),
					"attempt_count", row.AttemptCount+1,
					"next_attempt_in", backoff.String(),
					"error", err.Error())
			}
			continue
		}
		delivered++
	}
	return delivered, nil
}

// @decision: promotion-lineage-record-after-commit
func (d *ProducerVerbDispatcher) deliverRowAndClear(ctx context.Context, row persistence.ProducerVerbOutboxRow) error {
	instanceID := ""
	if row.InstanceID != nil {
		instanceID = row.InstanceID.String()
	}
	producer, ok, err := d.Producers.ResolveWithContext(ctx, row.ProducerName, instanceID, nil)
	if err != nil {
		return fmt.Errorf("resolving producer %q (transient): %w", row.ProducerName, err)
	}
	if !ok {
		return fmt.Errorf("producer %q not registered", row.ProducerName)
	}
	args := RunArgs{Persist: d.Tables, Clock: d.Clock, Logger: d.Logger}
	if d.Tables != nil {
		args.ClaimHandles = d.Tables.ClaimHandles()
	}
	clear := func(ctx context.Context, tx persistence.Tx) error {
		if err := d.Outbox.Delete(ctx, row.Seq, tx); err != nil {
			return fmt.Errorf("Delete(seq=%d): %w", row.Seq, err)
		}
		return nil
	}
	return deliverProducerVerb(ctx, args, producer, row, nil, clear)
}

type outboxRowFinalizeFn func(ctx context.Context, tx persistence.Tx) error

func deliverProducerVerb(
	ctx context.Context, args RunArgs, producer claimproducer.ClaimProducer,
	row persistence.ProducerVerbOutboxRow, tx persistence.Tx, finalize outboxRowFinalizeFn,
) error {
	ctx = peer.WithServiceName(ctx, row.ProducerName)
	claimID := claimproducer.ClaimID(row.ClaimHandleID.String())
	switch row.Verb {
	case persistence.ProducerVerbCommit:
		res, err := producer.Commit(ctx, claimID, row.ClaimScopeData, row.Address, row.LeaseToken)
		if err != nil {
			return err
		}
		return applyDeferredCommitResult(ctx, args, row, res, tx, finalize)
	case persistence.ProducerVerbAbandon:
		if err := producer.Abandon(ctx, claimID, row.ClaimScopeData, row.Address, row.LeaseToken); err != nil {
			return err
		}
		return runOutboxFinalize(ctx, args, tx, finalize)
	case persistence.ProducerVerbRelease:
		if err := producer.Release(ctx, claimID, row.ClaimScopeData, row.Address, row.LeaseToken); err != nil {
			return err
		}
		return runOutboxFinalize(ctx, args, tx, finalize)
	}
	return fmt.Errorf("unknown producer verb %q (seq=%d)", string(row.Verb), row.Seq)
}

func runOutboxFinalize(ctx context.Context, args RunArgs, tx persistence.Tx, finalize outboxRowFinalizeFn) error {
	if finalize == nil {
		return nil
	}
	if tx != nil {
		return finalize(ctx, tx)
	}
	if args.Persist == nil {
		return finalize(ctx, nil)
	}
	return args.Persist.Transaction(ctx, finalize)
}

// @decision: promotion-lineage-record-after-commit
func writePendingClaimTerminalLineage(
	ctx context.Context, args RunArgs, row persistence.ProducerVerbOutboxRow, versionID string, tx persistence.Tx,
) error {
	var rec ClaimTerminalRecord
	if err := json.Unmarshal(row.PendingLineageRecord, &rec); err != nil {
		return fmt.Errorf("decode staged lineage record: %w", err)
	}
	rec.VersionID = versionID
	instanceID := shared.UUID{}
	if row.InstanceID != nil {
		instanceID = *row.InstanceID
	}
	clock := args.Clock
	if clock == nil {
		clock = shared.SystemClock{}
	}
	return WriteClaimTerminalLineage(ctx, args.Persist.Lineage(), instanceID, rec.FrameID, clock.Now(), rec, tx)
}

// @decision: wire-commit-response-fields
func applyDeferredCommitResult(
	ctx context.Context, args RunArgs, row persistence.ProducerVerbOutboxRow,
	res claimproducer.CommitResult, tx persistence.Tx, finalize outboxRowFinalizeFn,
) error {
	needsVersion := res.VersionID != "" && args.ClaimHandles != nil
	needsMetadata := row.ParentClaimHandleID != nil && len(res.ProducerMetadata) > 0 &&
		args.ClaimHandles != nil && args.Persist != nil
	// @decision: promotion-lineage-record-after-commit
	needsLineage := len(row.PendingLineageRecord) > 0 && args.Persist != nil && args.Persist.Lineage() != nil
	if !needsVersion && !needsMetadata && !needsLineage {
		return runOutboxFinalize(ctx, args, tx, finalize)
	}
	apply := func(ctx context.Context, tx persistence.Tx) error {
		if needsVersion {
			if err := args.ClaimHandles.SetVersionID(ctx, row.ClaimHandleID, row.SupervisorID, res.VersionID, tx); err != nil {
				return fmt.Errorf("SetVersionID: %w", err)
			}
		}
		if needsLineage {
			if err := writePendingClaimTerminalLineage(ctx, args, row, res.VersionID, tx); err != nil {
				return err
			}
		}
		if needsMetadata {
			if err := applyChildCommitMetadata(ctx, args, row, res, tx); err != nil {
				return err
			}
		}
		if finalize == nil {
			return nil
		}
		return finalize(ctx, tx)
	}
	var err error
	if tx != nil {
		err = apply(ctx, tx)
	} else if args.Persist != nil {
		err = args.Persist.Transaction(ctx, apply)
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("applying deferred Commit result for claim_handle %s: %w", row.ClaimHandleID, err)
}

// @concept: fan-out
func applyChildCommitMetadata(
	ctx context.Context, args RunArgs, row persistence.ProducerVerbOutboxRow,
	res claimproducer.CommitResult, tx persistence.Tx,
) error {
	parent, err := args.ClaimHandles.Get(ctx, *row.ParentClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("load parent claim handle: %w", err)
	}
	if parent == nil {
		return nil
	}
	return recordChildCommitMetadata(ctx, args, FanoutChildSettlementInput{
		ParentClaimHandleID:   *row.ParentClaimHandleID,
		ChildClaimHandleID:    row.ClaimHandleID,
		ChildOutcome:          OutcomeCommit,
		ChildProducerMetadata: res.ProducerMetadata,
	}, parent, tx)
}

// @concept: claim-scope
func producerVerbOutboxBarrier(
	ctx context.Context, args RunArgs, producer claimproducer.ClaimProducer, producerName string, candidateScope []byte, tx persistence.Tx,
) error {
	outbox := ProducerVerbOutboxOf(args)
	if outbox == nil {
		return nil
	}
	rows, err := outbox.ListByProducer(ctx, producerName, tx)
	if err != nil {
		return fmt.Errorf("outbox barrier: ListByProducer(%s): %w", producerName, err)
	}
	if len(rows) == 0 {
		return nil
	}
	caps, err := producer.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("outbox barrier: Capabilities(%s): %w", producerName, err)
	}
	for _, row := range rows {
		conflicts, cErr := scopesConflict(ctx, producer, caps, candidateScope, row.ClaimScopeData)
		if cErr != nil {
			return fmt.Errorf("outbox barrier: ScopesConflict(%s): %w", producerName, cErr)
		}
		if !conflicts {
			continue
		}
		clear := func(ctx context.Context, tx persistence.Tx) error {
			if err := outbox.Delete(ctx, row.Seq, tx); err != nil {
				return fmt.Errorf("Delete(seq=%d): %w", row.Seq, err)
			}
			return nil
		}
		if err := deliverProducerVerb(ctx, args, producer, row, tx, clear); err != nil {
			return fmt.Errorf("outbox barrier: undelivered terminal %s (seq=%d) for producer %q blocks open: %w",
				string(row.Verb), row.Seq, producerName, err)
		}
	}
	return nil
}
