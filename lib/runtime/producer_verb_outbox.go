// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: terminal-resolution
// @concept: claim-producer

package runtime

import (
	"context"
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

func enqueueProducerVerb(
	ctx context.Context, args RunArgs, tx persistence.Tx, td TerminalDecision,
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
		ClaimHandleID:       td.ClaimHandleID,
		ProducerName:        producerName,
		Verb:                verb,
		ClaimScopeData:      td.Scope,
		Address:             td.Address,
		SupervisorID:        td.SupervisorID,
		InstanceID:          instanceID,
		ParentClaimHandleID: td.ParentClaimHandleID,
		NextAttemptAt:       now,
		EnqueuedAt:          now,
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
	d := NewProducerVerbDispatcher(outbox, args.Persist, args.StoreRegistry, clock, args.Logger)
	return d.DispatchOnce(ctx)
}

type ProducerVerbResolver interface {
	GetWithContext(ctx context.Context, name string, instanceID string) (claimproducer.ClaimProducer, bool)
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
			d.Logger.Warn("producer verb outbox: dispatch pass failed", "error", err.Error())
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
		if err := d.deliverRow(ctx, row); err != nil {
			blocked[key] = true
			backoff := producerVerbBackoff(row.AttemptCount+1, d.BaseBackoff, d.MaxBackoff)
			if rerr := d.Outbox.RecordAttempt(ctx, row.Seq, now.Add(backoff), err.Error(), nil); rerr != nil && d.Logger != nil {
				d.Logger.Warn("producer verb outbox: RecordAttempt failed",
					"seq", row.Seq, "producer", row.ProducerName, "error", rerr.Error())
			}
			if d.Logger != nil {
				d.Logger.Warn("producer verb outbox: delivery failed; retrying with backoff",
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
		if err := d.Outbox.Delete(ctx, row.Seq, nil); err != nil {
			return delivered, fmt.Errorf("ProducerVerbDispatcher: Delete(seq=%d): %w", row.Seq, err)
		}
		delivered++
	}
	return delivered, nil
}

func (d *ProducerVerbDispatcher) deliverRow(ctx context.Context, row persistence.ProducerVerbOutboxRow) error {
	instanceID := ""
	if row.InstanceID != nil {
		instanceID = row.InstanceID.String()
	}
	producer, ok := d.Producers.GetWithContext(ctx, row.ProducerName, instanceID)
	if !ok {
		return fmt.Errorf("producer %q not registered", row.ProducerName)
	}
	args := RunArgs{Persist: d.Tables, Clock: d.Clock, Logger: d.Logger}
	if d.Tables != nil {
		args.ClaimHandles = d.Tables.ClaimHandles()
	}
	return deliverProducerVerb(ctx, args, producer, row, nil)
}

func deliverProducerVerb(
	ctx context.Context, args RunArgs, producer claimproducer.ClaimProducer,
	row persistence.ProducerVerbOutboxRow, tx persistence.Tx,
) error {
	ctx = peer.WithServiceName(ctx, row.ProducerName)
	claimID := claimproducer.ClaimID(row.ClaimHandleID.String())
	switch row.Verb {
	case persistence.ProducerVerbCommit:
		res, err := producer.Commit(ctx, claimID, row.ClaimScopeData, row.Address)
		if err != nil {
			return err
		}
		applyDeferredCommitResult(ctx, args, tx, row, res)
		return nil
	case persistence.ProducerVerbAbandon:
		return producer.Abandon(ctx, claimID, row.ClaimScopeData, row.Address)
	case persistence.ProducerVerbRelease:
		return producer.Release(ctx, claimID, row.ClaimScopeData, row.Address)
	}
	return fmt.Errorf("unknown producer verb %q (seq=%d)", string(row.Verb), row.Seq)
}

func applyDeferredCommitResult(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row persistence.ProducerVerbOutboxRow, res claimproducer.CommitResult,
) {
	needsVersion := res.VersionID != "" && args.ClaimHandles != nil
	needsMetadata := row.ParentClaimHandleID != nil && len(res.ProducerMetadata) > 0 &&
		args.ClaimHandles != nil && args.Persist != nil
	if !needsVersion && !needsMetadata {
		return
	}
	apply := func(ctx context.Context, tx persistence.Tx) error {
		if needsVersion {
			if err := args.ClaimHandles.SetVersionID(ctx, row.ClaimHandleID, row.SupervisorID, res.VersionID, tx); err != nil {
				return fmt.Errorf("SetVersionID: %w", err)
			}
		}
		if !needsMetadata {
			return nil
		}
		parent, err := args.ClaimHandles.Get(ctx, *row.ParentClaimHandleID, tx)
		if err != nil {
			return fmt.Errorf("load parent claim handle: %w", err)
		}
		if parent == nil {
			return nil
		}
		// @concept: fan-out
		return recordChildCommitMetadata(ctx, args, tx, FanoutChildSettlementInput{
			ParentClaimHandleID:   *row.ParentClaimHandleID,
			ChildClaimHandleID:    row.ClaimHandleID,
			ChildOutcome:          OutcomeCommit,
			ChildProducerMetadata: res.ProducerMetadata,
		}, parent)
	}
	var err error
	if tx != nil {
		err = apply(ctx, tx)
	} else if args.Persist != nil {
		err = args.Persist.Transaction(ctx, apply)
	}
	if err != nil && args.Logger != nil {
		args.Logger.Warn("producer verb outbox: applying deferred Commit result failed",
			"claim_handle_id", row.ClaimHandleID.String(),
			"producer", row.ProducerName,
			"error", err.Error())
	}
}

// @concept: claim-scope
func producerVerbOutboxBarrier(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	producer claimproducer.ClaimProducer, producerName string, candidateScope []byte,
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
		if err := deliverProducerVerb(ctx, args, producer, row, tx); err != nil {
			return fmt.Errorf("outbox barrier: undelivered terminal %s (seq=%d) for producer %q blocks open: %w",
				string(row.Verb), row.Seq, producerName, err)
		}
		if err := outbox.Delete(ctx, row.Seq, tx); err != nil {
			return fmt.Errorf("outbox barrier: Delete(seq=%d): %w", row.Seq, err)
		}
	}
	return nil
}
