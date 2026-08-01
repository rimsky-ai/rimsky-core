// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package messages

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

type fakeInstancesForEnqueue struct {
	queueModes map[shared.UUID]string
}

func (f *fakeInstancesForEnqueue) Create(context.Context, persistence.InstanceCreateInput, persistence.Tx) (persistence.InstanceRow, error) {
	return persistence.InstanceRow{}, nil
}
func (f *fakeInstancesForEnqueue) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.InstanceRow, error) {
	return &persistence.InstanceRow{ID: id, MessageQueueMode: f.queueModes[id]}, nil
}
func (f *fakeInstancesForEnqueue) GetByInstanceKey(context.Context, string, string, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fakeInstancesForEnqueue) FindAnyByInstanceKey(context.Context, string, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fakeInstancesForEnqueue) List(context.Context, persistence.InstanceListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.InstanceRow], error) {
	return persistence.PaginatedListResult[persistence.InstanceRow]{}, nil
}
func (f *fakeInstancesForEnqueue) Delete(context.Context, shared.UUID, persistence.Tx) error {
	return nil
}
func (f *fakeInstancesForEnqueue) MarkTerminated(context.Context, shared.UUID, persistence.Tx) error {
	return nil
}
func (f *fakeInstancesForEnqueue) CountActiveByTemplate(context.Context, string, persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeInstancesForEnqueue) ListTerminatedWithLifecycleRows(context.Context, int, persistence.Tx) ([]persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fakeInstancesForEnqueue) CountByActive(context.Context, persistence.Tx) (int, int, error) {
	return 0, 0, nil
}
func (f *fakeInstancesForEnqueue) SetPaused(context.Context, shared.UUID, bool, persistence.Tx) (bool, error) {
	return false, nil
}

type fakeTemplatesForEnqueue struct{}

func (f *fakeTemplatesForEnqueue) Insert(context.Context, persistence.TemplateInsertInput, persistence.Tx) error {
	return nil
}
func (f *fakeTemplatesForEnqueue) GetByHash(context.Context, string, persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, nil
}
func (f *fakeTemplatesForEnqueue) List(context.Context, persistence.TemplateListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.TemplateRow], error) {
	return persistence.PaginatedListResult[persistence.TemplateRow]{}, nil
}
func (f *fakeTemplatesForEnqueue) UpdateState(context.Context, string, persistence.TemplateState, persistence.Tx) error {
	return nil
}
func (f *fakeTemplatesForEnqueue) DeleteByHash(context.Context, string, persistence.Tx) error {
	return nil
}
func (f *fakeTemplatesForEnqueue) LockForUpdate(context.Context, string, persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, nil
}

type fakeEnqueueDeps struct {
	msgs       *fakeMessages
	queueModes map[shared.UUID]string
}

func (d *fakeEnqueueDeps) Instances() persistence.InstanceTable {
	return &fakeInstancesForEnqueue{queueModes: d.queueModes}
}
func (d *fakeEnqueueDeps) Templates() persistence.TemplateTable { return &fakeTemplatesForEnqueue{} }
func (d *fakeEnqueueDeps) Messages() persistence.MessageTable   { return d.msgs }

func (f *fakeMessages) Insert(_ context.Context, req persistence.EnqueueMessageRequest, _ persistence.Tx) error {
	f.rows[req.ID] = &persistence.MessageRow{
		ID:         req.ID,
		InstanceID: req.InstanceID,
		Type:       req.Type,
		Sender:     req.Sender,
		SenderKind: req.SenderKind,
		Payload:    req.Payload,
		ReceivedAt: req.ReceivedAt,
	}
	return nil
}

func (f *fakeMessages) MarkDelivered(_ context.Context, id shared.UUID, frame shared.UUID, deliveredAt time.Time, _ persistence.Tx) (bool, error) {
	row, ok := f.rows[id]
	if !ok || row.DeliveredAt != nil {
		return false, nil
	}
	row.DeliveredAt = &deliveredAt
	fid := frame
	row.FrameID = &fid
	return true, nil
}

func (f *fakeMessages) ListPendingForInstance(_ context.Context, instanceID shared.UUID, _ persistence.Tx) ([]persistence.MessageRow, error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if r.InstanceID != instanceID || r.DeliveredAt != nil || r.Cancelled {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.Before(out[j].ReceivedAt) })
	return out, nil
}

func (f *fakeMessages) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.MessageRow, error) {
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

func (f *fakeMessages) CancelPendingForInstance(_ context.Context, instanceID shared.UUID, _ persistence.Tx) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.InstanceID == instanceID && r.DeliveredAt == nil && !r.Cancelled {
			r.Cancelled = true
			n++
		}
	}
	return n, nil
}

func (f *fakeMessages) PickPendingMessagesForIdleInstances(_ context.Context, _ persistence.Tx) ([]persistence.PendingMessagePick, error) {
	return nil, nil
}

func (f *fakeMessages) ListDeliveredForFrame(_ context.Context, frame shared.UUID, _ persistence.Tx) ([]persistence.MessageRow, error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if r.FrameID == nil || *r.FrameID != frame {
			continue
		}
		out = append(out, *r)
	}
	return out, nil
}
