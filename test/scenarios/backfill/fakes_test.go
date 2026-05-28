// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Shared fakes for the N9 backfill scenarios.
//
// @source: runtime/message_delivery_test.go::fakeMessagesTable
// @diverged: false
package backfill

import (
	"context"
	"sort"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type fakeMessages struct {
	rows map[shared.UUID]*persistence.MessageRow
}

func newFakeMessages() *fakeMessages {
	return &fakeMessages{rows: make(map[shared.UUID]*persistence.MessageRow)}
}

func (f *fakeMessages) Insert(_ context.Context, _ persistence.Tx, req persistence.EnqueueMessageRequest) error {
	f.rows[req.ID] = &persistence.MessageRow{
		ID:                  req.ID,
		InstanceID:          req.InstanceID,
		Kind:                req.Kind,
		Sender:              req.Sender,
		SenderKind:          req.SenderKind,
		Target:              req.Target,
		Payload:             req.Payload,
		BackfillOperationID: req.BackfillOperationID,
		ReceivedAt:          req.ReceivedAt,
	}
	return nil
}

func (f *fakeMessages) MarkDelivered(_ context.Context, _ persistence.Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error) {
	row, ok := f.rows[id]
	if !ok || row.DeliveredAt != nil {
		return false, nil
	}
	row.DeliveredAt = &deliveredAt
	fid := frame
	row.FrameID = &fid
	return true, nil
}

func (f *fakeMessages) MarkCancelled(_ context.Context, _ persistence.Tx, op shared.UUID, at time.Time) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.BackfillOperationID != nil && *r.BackfillOperationID == op && r.DeliveredAt == nil {
			r.Cancelled = true
			t := at
			r.DeliveredAt = &t
			n++
		}
	}
	return n, nil
}

func (f *fakeMessages) ListPendingForInstance(_ context.Context, _ persistence.Tx, instanceID shared.UUID) ([]persistence.MessageRow, error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if r.InstanceID != instanceID || r.DeliveredAt != nil {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.Before(out[j].ReceivedAt) })
	return out, nil
}

func (f *fakeMessages) Get(_ context.Context, id shared.UUID) (*persistence.MessageRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeMessages) List(_ context.Context, filter persistence.MessageListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if filter.BackfillOperationID != nil {
			if r.BackfillOperationID == nil || *r.BackfillOperationID != *filter.BackfillOperationID {
				continue
			}
		}
		out = append(out, *r)
	}
	return persistence.PaginatedListResult[persistence.MessageRow]{Rows: out}, nil
}
