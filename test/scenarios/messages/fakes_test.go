// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Shared test fixtures for the N4 message-delivery scenarios.
// Mirrors the in-memory fake under runtime/message_delivery_test.go
// — duplicated here per cold-read rule "tracked duplication over
// hidden coupling".
//
// @source: runtime/message_delivery_test.go::fakeMessagesTable
// @diverged: false
package messages

import (
	"context"
	"sort"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
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

func (f *fakeMessages) List(_ context.Context, _ persistence.MessageListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	return persistence.PaginatedListResult[persistence.MessageRow]{}, nil
}
