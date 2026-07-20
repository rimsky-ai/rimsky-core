// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type fakeMessagesTable struct {
	rows map[shared.UUID]*persistence.MessageRow
}

func newFakeMessages() *fakeMessagesTable {
	return &fakeMessagesTable{rows: make(map[shared.UUID]*persistence.MessageRow)}
}

func (f *fakeMessagesTable) Insert(_ context.Context, _ persistence.Tx, req persistence.EnqueueMessageRequest) error {
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

func (f *fakeMessagesTable) MarkDelivered(_ context.Context, _ persistence.Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error) {
	row, ok := f.rows[id]
	if !ok || row.DeliveredAt != nil {
		return false, nil
	}
	row.DeliveredAt = &deliveredAt
	fid := frame
	row.FrameID = &fid
	return true, nil
}

func (f *fakeMessagesTable) ListPendingForInstance(_ context.Context, _ persistence.Tx, instanceID shared.UUID) ([]persistence.MessageRow, error) {
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

func (f *fakeMessagesTable) ListDeliveredForFrame(_ context.Context, _ persistence.Tx, frame shared.UUID) ([]persistence.MessageRow, error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if r.FrameID == nil || *r.FrameID != frame {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.Before(out[j].ReceivedAt) })
	return out, nil
}

func (f *fakeMessagesTable) Get(_ context.Context, id shared.UUID) (*persistence.MessageRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeMessagesTable) GetInTx(ctx context.Context, _ persistence.Tx, id shared.UUID) (*persistence.MessageRow, error) {
	return f.Get(ctx, id)
}

func (f *fakeMessagesTable) CancelPendingForInstance(_ context.Context, _ persistence.Tx, instanceID shared.UUID) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.InstanceID == instanceID && r.DeliveredAt == nil && !r.Cancelled {
			r.Cancelled = true
			n++
		}
	}
	return n, nil
}

func (f *fakeMessagesTable) PickPendingMessagesForIdleInstances(_ context.Context, _ persistence.Tx) ([]persistence.PendingMessagePick, error) {
	return nil, nil
}

func (f *fakeMessagesTable) List(_ context.Context, filter persistence.MessageListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if filter.InstanceID != nil && r.InstanceID != *filter.InstanceID {
			continue
		}
		if filter.FrameID != nil {
			if r.FrameID == nil || *r.FrameID != *filter.FrameID {
				continue
			}
		}
		out = append(out, *r)
		if pag.Limit > 0 && len(out) >= pag.Limit {
			break
		}
	}
	return persistence.PaginatedListResult[persistence.MessageRow]{Rows: out}, nil
}

type enqueueDepsStub struct {
	inst persistence.InstanceTable
	tpls persistence.TemplateTable
	msgs persistence.MessagesTable
}

func (d *enqueueDepsStub) Instances() persistence.InstanceTable { return d.inst }
func (d *enqueueDepsStub) Templates() persistence.TemplateTable { return d.tpls }
func (d *enqueueDepsStub) Messages() persistence.MessagesTable  { return d.msgs }

type nilTemplatesTable struct{}

func (n *nilTemplatesTable) Insert(context.Context, persistence.TemplateInsertInput, persistence.Tx) error {
	return nil
}
func (n *nilTemplatesTable) GetByHash(context.Context, string, persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, nil
}
func (n *nilTemplatesTable) List(context.Context, persistence.TemplateListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.TemplateRow], error) {
	return persistence.PaginatedListResult[persistence.TemplateRow]{}, nil
}
func (n *nilTemplatesTable) UpdateState(context.Context, string, persistence.TemplateState, persistence.Tx) error {
	return nil
}
func (n *nilTemplatesTable) DeleteByHash(context.Context, string, persistence.Tx) error { return nil }
func (n *nilTemplatesTable) LockForUpdate(context.Context, string, persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, nil
}

type fixedInstanceTable struct {
	row *persistence.InstanceRow
}

func (f *fixedInstanceTable) Create(context.Context, persistence.InstanceCreateInput, persistence.Tx) (persistence.InstanceRow, error) {
	return persistence.InstanceRow{}, nil
}
func (f *fixedInstanceTable) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.InstanceRow, error) {
	if f.row == nil || f.row.ID != id {
		return nil, nil
	}
	cp := *f.row
	return &cp, nil
}
func (f *fixedInstanceTable) GetByInstanceKey(context.Context, string, string, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fixedInstanceTable) FindAnyByInstanceKey(context.Context, string, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fixedInstanceTable) List(context.Context, persistence.InstanceListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.InstanceRow], error) {
	return persistence.PaginatedListResult[persistence.InstanceRow]{}, nil
}
func (f *fixedInstanceTable) Delete(context.Context, shared.UUID, persistence.Tx) error { return nil }
func (f *fixedInstanceTable) MarkTerminated(context.Context, shared.UUID, persistence.Tx) error {
	return nil
}
func (f *fixedInstanceTable) CountActiveByTemplate(context.Context, string, persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fixedInstanceTable) ListTerminatedWithLifecycleRows(context.Context, int, persistence.Tx) ([]persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fixedInstanceTable) CountByActive(context.Context, persistence.Tx) (int, int, error) {
	return 0, 0, nil
}
func (f *fixedInstanceTable) SetPaused(context.Context, shared.UUID, bool, persistence.Tx) (bool, error) {
	return false, nil
}

type fixedTemplateTable struct {
	row *persistence.TemplateRow
}

func (f *fixedTemplateTable) Insert(context.Context, persistence.TemplateInsertInput, persistence.Tx) error {
	return nil
}
func (f *fixedTemplateTable) GetByHash(_ context.Context, hash string, _ persistence.Tx) (*persistence.TemplateRow, error) {
	if f.row == nil || f.row.ID != hash {
		return nil, nil
	}
	cp := *f.row
	return &cp, nil
}
func (f *fixedTemplateTable) List(context.Context, persistence.TemplateListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.TemplateRow], error) {
	return persistence.PaginatedListResult[persistence.TemplateRow]{}, nil
}
func (f *fixedTemplateTable) UpdateState(context.Context, string, persistence.TemplateState, persistence.Tx) error {
	return nil
}
func (f *fixedTemplateTable) DeleteByHash(context.Context, string, persistence.Tx) error { return nil }
func (f *fixedTemplateTable) LockForUpdate(context.Context, string, persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, nil
}

type nilInstancesTable struct{}

func (n *nilInstancesTable) Create(context.Context, persistence.InstanceCreateInput, persistence.Tx) (persistence.InstanceRow, error) {
	return persistence.InstanceRow{}, nil
}
func (n *nilInstancesTable) Get(context.Context, shared.UUID, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (n *nilInstancesTable) GetByInstanceKey(context.Context, string, string, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (n *nilInstancesTable) FindAnyByInstanceKey(context.Context, string, persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (n *nilInstancesTable) List(context.Context, persistence.InstanceListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.InstanceRow], error) {
	return persistence.PaginatedListResult[persistence.InstanceRow]{}, nil
}
func (n *nilInstancesTable) Delete(context.Context, shared.UUID, persistence.Tx) error { return nil }
func (n *nilInstancesTable) MarkTerminated(context.Context, shared.UUID, persistence.Tx) error {
	return nil
}
func (n *nilInstancesTable) CountActiveByTemplate(context.Context, string, persistence.Tx) (int, error) {
	return 0, nil
}
func (n *nilInstancesTable) ListTerminatedWithLifecycleRows(context.Context, int, persistence.Tx) ([]persistence.InstanceRow, error) {
	return nil, nil
}
func (n *nilInstancesTable) CountByActive(context.Context, persistence.Tx) (int, int, error) {
	return 0, 0, nil
}
func (n *nilInstancesTable) SetPaused(context.Context, shared.UUID, bool, persistence.Tx) (bool, error) {
	return false, nil
}

func TestEnqueueMessage_ValidatesShape(t *testing.T) {
	m := newFakeMessages()
	deps := &enqueueDepsStub{inst: &nilInstancesTable{}, tpls: &nilTemplatesTable{}, msgs: m}
	ctx := context.Background()
	good := persistence.EnqueueMessageRequest{
		ID: shared.UUID(uuid.New()), InstanceID: shared.UUID(uuid.New()),
		Type: "invalidate", Sender: "op-A", SenderKind: "operator",
	}
	if err := EnqueueMessage(ctx, nil, deps, good); err != nil {
		t.Fatalf("EnqueueMessage(good): %v", err)
	}

	if err := EnqueueMessage(ctx, nil, deps, persistence.EnqueueMessageRequest{}); err == nil {
		t.Fatal("EnqueueMessage(empty): expected error")
	}

	bad := good
	bad.ID = shared.UUID(uuid.New())
	bad.SenderKind = "bogus"
	if err := EnqueueMessage(ctx, nil, deps, bad); err == nil {
		t.Fatal("EnqueueMessage(bogus sender_kind): expected error")
	}
}

// @concept: message-schema
func TestEnqueueMessage_RejectsBodySchemaViolation(t *testing.T) {
	m := newFakeMessages()
	instanceID := shared.UUID(uuid.New())
	instRow := &persistence.InstanceRow{ID: instanceID, TemplateHash: "tpl-1"}
	tplRow := &persistence.TemplateRow{
		ID: "tpl-1",
		Spec: spec.TemplateSpec{
			Name: "t", Version: "v1",
			Messages: []spec.MessageSchema{
				{
					Type:       "ping/recheck",
					BodySchema: []byte(`{"type":"object","properties":{"pong_status":{"type":"string"}},"required":["pong_status"]}`),
				},
			},
		},
	}
	deps := &enqueueDepsStub{
		inst: &fixedInstanceTable{row: instRow},
		tpls: &fixedTemplateTable{row: tplRow},
		msgs: m,
	}
	ctx := context.Background()

	violating := persistence.EnqueueMessageRequest{
		ID: shared.UUID(uuid.New()), InstanceID: instanceID,
		Type: "ping/recheck", Sender: "op-A", SenderKind: "operator",
		Payload: []byte(`{}`),
	}
	err := EnqueueMessage(ctx, nil, deps, violating)
	if err == nil {
		t.Fatal("EnqueueMessage(body violating body_schema): expected error, got nil")
	}
	var schemaErr *node.MessageBodySchemaViolation
	if !errors.As(err, &schemaErr) {
		t.Fatalf("EnqueueMessage error must unwrap to *node.MessageBodySchemaViolation, got %T: %v", err, err)
	}
	if schemaErr.Type != "ping/recheck" {
		t.Fatalf("schemaErr.Type = %q, want %q", schemaErr.Type, "ping/recheck")
	}
	if _, ok := m.rows[violating.ID]; ok {
		t.Fatal("a body-schema-rejected message must not be inserted into the ledger")
	}

	compliant := violating
	compliant.ID = shared.UUID(uuid.New())
	compliant.Payload = []byte(`{"pong_status":"ok"}`)
	if err := EnqueueMessage(ctx, nil, deps, compliant); err != nil {
		t.Fatalf("EnqueueMessage(body matching body_schema): %v", err)
	}
	if _, ok := m.rows[compliant.ID]; !ok {
		t.Fatal("a schema-compliant message must be inserted into the ledger")
	}
}

func TestDeliverPendingMessages_OnlyDeliversFrameTrigger(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame1 := shared.UUID(uuid.New())
	now := time.Now().UTC()

	triggerID := shared.UUID(uuid.New())
	siblingID := shared.UUID(uuid.New())
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: triggerID, InstanceID: inst, Type: "invalidate",
		Sender: "op-A", SenderKind: "operator", ReceivedAt: now,
	})
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: siblingID, InstanceID: inst, Type: "invalidate",
		Sender: "op-A", SenderKind: "operator",
		ReceivedAt: now.Add(time.Second),
	})

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame1, triggerID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != triggerID {
		t.Fatalf("DeliverPendingMessages: got %+v, want exactly the trigger %s", res.Messages, triggerID)
	}

	remaining, _ := m.ListPendingForInstance(ctx, nil, inst)
	if len(remaining) != 1 || remaining[0].ID != siblingID {
		t.Fatalf("after delivering trigger, pending=%v, want one row with sibling %s — a message must only flow into its own frame, never into a sibling frame's delivery",
			remaining, siblingID)
	}

	res, err = DeliverPendingMessages(ctx, nil, m, inst, frame1, triggerID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages (repeat): %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected empty deliver-set on repeat call (idempotent), got %d", len(res.Messages))
	}
}

func TestDeliverPendingMessages_DeliveredMatchesTrigger(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	msgID := shared.UUID(uuid.New())
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: msgID, InstanceID: inst, Type: "ping/recheck",
		Sender: "op-A", SenderKind: "operator", ReceivedAt: now,
	})

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, msgID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != msgID {
		t.Fatalf("DeliverPendingMessages: got %+v, want one row with id=%s", res.Messages, msgID)
	}

	delivered, err := m.ListDeliveredForFrame(ctx, nil, frame)
	if err != nil {
		t.Fatalf("ListDeliveredForFrame: %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("ListDeliveredForFrame: got %d rows, want exactly 1", len(delivered))
	}
	if delivered[0].ID != msgID {
		t.Fatalf("ListDeliveredForFrame row id = %s; want the delivered message id %s — substitution and frame-origin audit must read the same envelope",
			delivered[0].ID, msgID)
	}
}
