// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testMessagesListByFrameID(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	frameA := fix.FrameID
	frameBMsgID := shared.UUID(uuid.New())
	var frameB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := store.Frames().MarkFrameEnded(ctx, frameA, tx); err != nil {
			return err
		}
		if err := store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         frameBMsgID,
			InstanceID: fix.InstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameBScope := seedMainRunScopeForInstance(ctx, t, store, fix.InstanceID, tx)
		fid, err := store.Frames().InsertRunningFrame(ctx, fix.InstanceID, frameBMsgID, frameBScope, tx)
		if err != nil {
			return err
		}
		frameB = fid
		return nil
	}); err != nil {
		t.Fatalf("seed frameB: %v", err)
	}

	enqueueAndDeliver := func(t *testing.T, frame shared.UUID, payload map[string]any) shared.UUID {
		t.Helper()
		msgID := shared.UUID(uuid.New())
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
				ID:         msgID,
				InstanceID: fix.InstanceID,
				Type:       "fixture/message",
				Sender:     "operator",
				SenderKind: "operator",
				Payload:    body,
				ReceivedAt: time.Now().UTC(),
			}, tx)
		}); err != nil {
			t.Fatalf("Messages.Insert: %v", err)
		}
		if frame != (shared.UUID{}) {
			if err := inTx(ctx, store, func(tx persistence.Tx) error {
				ok, err := store.Messages().MarkDelivered(ctx, msgID, frame, time.Now().UTC(), tx)
				if err != nil {
					return err
				}
				if !ok {
					t.Fatalf("MarkDelivered: expected one row updated for %s", msgID)
				}
				return nil
			}); err != nil {
				t.Fatalf("Messages.MarkDelivered: %v", err)
			}
		}
		return msgID
	}

	msgA := enqueueAndDeliver(t, frameA, map[string]any{"partition_request_override": map[string]any{"a": 1}})
	msgB := enqueueAndDeliver(t, frameB, map[string]any{"partition_request_override": map[string]any{"b": 2}})
	msgC := enqueueAndDeliver(t, shared.UUID{}, map[string]any{"partition_request_override": map[string]any{"c": 3}})

	list := func(f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		res, err := store.Messages().List(ctx, f, persistence.ListPagination{Limit: 50})
		if err != nil {
			t.Fatalf("Messages.List: %v", err)
		}
		return res.Rows
	}

	if got := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID}); len(got) != 5 {
		t.Fatalf("no FrameID filter = %d rows, want 5", len(got))
	}

	gotA := list(persistence.MessageListFilter{FrameID: &frameA})
	if len(gotA) != 1 {
		t.Fatalf("FrameID(A) = %d rows, want 1", len(gotA))
	}
	if gotA[0].ID != msgA {
		t.Fatalf("FrameID(A) returned %s, want %s", gotA[0].ID, msgA)
	}
	if gotA[0].FrameID == nil || *gotA[0].FrameID != frameA {
		t.Fatalf("FrameID(A) row frame_id mismatch: %v", gotA[0].FrameID)
	}

	gotB := list(persistence.MessageListFilter{FrameID: &frameB})
	if len(gotB) != 1 {
		t.Fatalf("FrameID(B) = %d rows, want 1", len(gotB))
	}
	if gotB[0].ID != msgB {
		t.Fatalf("FrameID(B) returned %s, want %s", gotB[0].ID, msgB)
	}

	unknownFrame := shared.UUID(uuid.New())
	if got := list(persistence.MessageListFilter{FrameID: &unknownFrame}); len(got) != 0 {
		t.Fatalf("FrameID(unknown) = %d rows, want 0", len(got))
	}

	pending := true
	settled := false
	gotPending := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Pending: &pending})
	gotSettled := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Pending: &settled})
	if len(gotPending)+len(gotSettled) != 5 {
		t.Fatalf("Pending=true (%d) + Pending=false (%d) must partition all 5 rows",
			len(gotPending), len(gotSettled))
	}
	inRows := func(rows []persistence.MessageRow, id shared.UUID) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	if !inRows(gotPending, msgC) {
		t.Fatalf("undelivered msgC must appear under Pending=true; pending rows = %v", gotPending)
	}
	if !inRows(gotSettled, msgA) || !inRows(gotSettled, msgB) {
		t.Fatalf("delivered msgA and msgB must appear under Pending=false; settled rows = %v", gotSettled)
	}
	if !inRows(gotPending, fix.MessageID) || !inRows(gotPending, frameBMsgID) {
		t.Fatalf("the two frame-triggering messages are never marked delivered and must appear under "+
			"Pending=true; pending rows = %v", gotPending)
	}
	if len(gotPending) != 3 {
		t.Fatalf("Pending=true = %d rows, want exactly 3 (msgC + the two frame-triggering messages)", len(gotPending))
	}
	if len(gotSettled) != 2 {
		t.Fatalf("Pending=false = %d rows, want exactly 2 (msgA + msgB)", len(gotSettled))
	}
	for _, r := range gotPending {
		if inRows(gotSettled, r.ID) {
			t.Fatalf("message %s appears in both Pending=true and Pending=false results", r.ID)
		}
	}

	var deliveredA []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Messages().ListDeliveredForFrame(ctx, frameA, tx)
		deliveredA = r
		return err
	}); err != nil {
		t.Fatalf("ListDeliveredForFrame(A) inside tx: %v", err)
	}
	if len(deliveredA) != 1 || deliveredA[0].ID != msgA {
		t.Fatalf("ListDeliveredForFrame(A) = %v, want exactly msgA=%s", deliveredA, msgA)
	}
	var deliveredUnknown []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Messages().ListDeliveredForFrame(ctx, unknownFrame, tx)
		deliveredUnknown = r
		return err
	}); err != nil {
		t.Fatalf("ListDeliveredForFrame(unknown) inside tx: %v", err)
	}
	if len(deliveredUnknown) != 0 {
		t.Fatalf("ListDeliveredForFrame(unknown) = %d rows, want 0", len(deliveredUnknown))
	}
}

// @concept: message
// @decision: message-sender-kind-discriminator
func testMessagesSenderSubjectRoundTrips(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	operatorID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:            operatorID,
			InstanceID:    fix.InstanceID,
			Type:          "fixture/message",
			Sender:        "operator",
			SenderKind:    "operator",
			SenderSubject: "api-key-A",
			ReceivedAt:    time.Now().UTC(),
		}, tx); err != nil {
			return err
		}
		return store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         instanceID,
			InstanceID: fix.InstanceID,
			Type:       "fixture/message",
			Sender:     "instance:" + fix.InstanceID.String(),
			SenderKind: "instance",
			ReceivedAt: time.Now().UTC(),
		}, tx)
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}

	want := map[shared.UUID]string{operatorID: "api-key-A", instanceID: ""}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		for id, subject := range want {
			row, err := store.Messages().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			if row == nil {
				t.Fatalf("Messages.Get(%s) returned no row", id)
			}
			if row.SenderSubject != subject {
				t.Fatalf("Messages.Get(%s) sender_subject = %q, want %q", id, row.SenderSubject, subject)
			}
		}
		pending, err := store.Messages().ListPendingForInstance(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		seen := 0
		for _, row := range pending {
			subject, tracked := want[row.ID]
			if !tracked {
				continue
			}
			seen++
			if row.SenderSubject != subject {
				t.Fatalf("ListPendingForInstance sender_subject for %s = %q, want %q", row.ID, row.SenderSubject, subject)
			}
		}
		if seen != len(want) {
			t.Fatalf("ListPendingForInstance returned %d of the %d seeded envelopes", seen, len(want))
		}
		return nil
	}); err != nil {
		t.Fatalf("read back sender_subject: %v", err)
	}

	listed, err := store.Messages().List(ctx,
		persistence.MessageListFilter{InstanceID: &fix.InstanceID},
		persistence.ListPagination{Limit: 50})
	if err != nil {
		t.Fatalf("Messages.List: %v", err)
	}
	for _, row := range listed.Rows {
		subject, tracked := want[row.ID]
		if !tracked {
			continue
		}
		if row.SenderSubject != subject {
			t.Fatalf("Messages.List sender_subject for %s = %q, want %q", row.ID, row.SenderSubject, subject)
		}
	}
}

// @concept: message
func testMessagesMarkDeliveredExcludesCancelled(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	msgID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: fix.InstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		}, tx)
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		n, err := store.Messages().CancelPendingForInstance(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if n < 1 {
			t.Fatalf("CancelPendingForInstance: cancelled %d rows, want at least 1", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("Messages.CancelPendingForInstance: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.Messages().MarkDelivered(ctx, msgID, fix.FrameID, time.Now().UTC(), tx)
		if err != nil {
			return err
		}
		if ok {
			t.Fatalf("MarkDelivered must not affect a cancelled message (coalesce-cancelled messages must never be delivered)")
		}
		return nil
	}); err != nil {
		t.Fatalf("Messages.MarkDelivered: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		row, err := store.Messages().Get(ctx, msgID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			t.Fatalf("expected message row to still exist")
		}
		if row.DeliveredAt != nil {
			t.Fatalf("a cancelled message's delivered_at must remain nil after a rejected MarkDelivered, got %v", row.DeliveredAt)
		}
		if !row.Cancelled {
			t.Fatalf("message must still be marked cancelled")
		}
		return nil
	}); err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
}

// @concept: observability
func testMessagesListBySender(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	insert := func(sender, senderKind string) shared.UUID {
		t.Helper()
		id := shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
				ID:         id,
				InstanceID: fix.InstanceID,
				Type:       "fixture/message",
				Sender:     sender,
				SenderKind: senderKind,
				ReceivedAt: time.Now().UTC(),
			}, tx)
		}); err != nil {
			t.Fatalf("Messages.Insert(sender=%s): %v", sender, err)
		}
		return id
	}

	msgA := insert("publisher-a", "publisher")
	msgB := insert("publisher-b", "publisher")
	insert("operator", "operator")

	list := func(f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		res, err := store.Messages().List(ctx, f, persistence.ListPagination{Limit: 50})
		if err != nil {
			t.Fatalf("Messages.List: %v", err)
		}
		return res.Rows
	}

	gotA := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Sender: "publisher-a"})
	if len(gotA) != 1 || gotA[0].ID != msgA {
		t.Fatalf("Sender(publisher-a) = %v, want exactly [%s]", gotA, msgA)
	}

	gotB := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Sender: "publisher-b"})
	if len(gotB) != 1 || gotB[0].ID != msgB {
		t.Fatalf("Sender(publisher-b) = %v, want exactly [%s]", gotB, msgB)
	}

	gotUnknown := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Sender: "publisher-nonexistent"})
	if len(gotUnknown) != 0 {
		t.Fatalf("Sender(publisher-nonexistent) = %d rows, want 0", len(gotUnknown))
	}

	publisherKind := "publisher"
	gotKindOnly := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, SenderKind: &publisherKind})
	if len(gotKindOnly) != 2 {
		t.Fatalf("SenderKind(publisher) = %d rows, want 2 (both publisher-a and publisher-b)", len(gotKindOnly))
	}
}

func testMessagesScanNullPayload(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	messages := store.Messages()
	msgID := shared.UUID(uuid.New())

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return messages.Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: fix.InstanceID,
			Type:       "invalidate",
			Sender:     "operator",
			SenderKind: "operator",
		}, tx)
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}

	var row *persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, gerr := messages.Get(ctx, msgID, tx)
		row = r
		return gerr
	}); err != nil {
		t.Fatalf("Messages.Get: %v (NULL payload must scan without error)", err)
	}
	if row == nil {
		t.Fatalf("Messages.Get returned nil for an inserted row")
	}
	if len(row.Payload) != 0 {
		t.Fatalf("payload bytes = %q; want zero-length (NULL column → nil/empty json.RawMessage)", string(row.Payload))
	}

	page, err := messages.List(ctx, persistence.MessageListFilter{InstanceID: &fix.InstanceID}, persistence.ListPagination{Limit: 50})
	if err != nil {
		t.Fatalf("Messages.List: %v (NULL payload must scan in List path)", err)
	}
	var listRow *persistence.MessageRow
	for i := range page.Rows {
		if page.Rows[i].ID == msgID {
			listRow = &page.Rows[i]
		}
	}
	if listRow == nil {
		t.Fatalf("List did not return the inserted message %s among %v", msgID, page.Rows)
	}
	if len(listRow.Payload) != 0 {
		t.Fatalf("List row payload bytes = %q; want zero-length", string(listRow.Payload))
	}

	var pending []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := messages.ListPendingForInstance(ctx, fix.InstanceID, tx)
		pending = r
		return err
	}); err != nil {
		t.Fatalf("Messages.ListPendingForInstance: %v (NULL payload must scan)", err)
	}
	var pendingRow *persistence.MessageRow
	for i := range pending {
		if pending[i].ID == msgID {
			pendingRow = &pending[i]
		}
	}
	if pendingRow == nil {
		t.Fatalf("ListPendingForInstance did not return the inserted message %s among %v", msgID, pending)
	}
	if len(pendingRow.Payload) != 0 {
		t.Fatalf("pending row payload bytes = %q; want zero-length", string(pendingRow.Payload))
	}
}

func testMessagesScanNonNullPayload(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	messages := store.Messages()
	msgID := shared.UUID(uuid.New())

	payload := json.RawMessage(`{"hello":"world"}`)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return messages.Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: fix.InstanceID,
			Type:       "invalidate",
			Sender:     "operator",
			SenderKind: "operator",
			Payload:    payload,
		}, tx)
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}
	var row *persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, gerr := messages.Get(ctx, msgID, tx)
		row = r
		return gerr
	}); err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("Messages.Get returned nil")
	}
	if string(row.Payload) != string(payload) {
		t.Fatalf("payload round-trip: got %q, want %q", string(row.Payload), string(payload))
	}
}

func testMessagesListDeliveredAfterBefore(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	messages := store.Messages()

	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ids := make([]shared.UUID, 3)
	for i, tt := range []time.Time{t1, t2, t3} {
		ids[i] = shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			if err := messages.Insert(ctx, persistence.EnqueueMessageRequest{
				ID:         ids[i],
				InstanceID: fix.InstanceID,
				Type:       "ping/recheck",
				Sender:     "operator",
				SenderKind: "operator",
				ReceivedAt: tt.Add(-time.Hour),
			}, tx); err != nil {
				return err
			}
			ok, err := messages.MarkDelivered(ctx, ids[i], fix.FrameID, tt, tx)
			if err != nil {
				return err
			}
			if !ok {
				t.Fatalf("MarkDelivered: expected one row updated for %s", ids[i])
			}
			return nil
		}); err != nil {
			t.Fatalf("Messages.Insert/MarkDelivered[%d]: %v", i, err)
		}
	}

	pendingID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return messages.Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         pendingID,
			InstanceID: fix.InstanceID,
			Type:       "ping/recheck",
			Sender:     "operator",
			SenderKind: "operator",
			ReceivedAt: t2,
		}, tx)
	}); err != nil {
		t.Fatalf("Messages.Insert(pending): %v", err)
	}

	list := func(name string, f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		f.InstanceID = &fix.InstanceID
		page, err := messages.List(ctx, f, persistence.ListPagination{Limit: 10})
		if err != nil {
			t.Fatalf("List(%s): %v", name, err)
		}
		return page.Rows
	}
	holds := func(rows []persistence.MessageRow, id shared.UUID) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	boundary := t2
	afterRows := list("delivered_after=t2", persistence.MessageListFilter{DeliveredAfter: &boundary})
	if !holds(afterRows, ids[1]) {
		t.Fatalf("a message delivered exactly at the window's start belongs in the window; rows=%+v", afterRows)
	}
	if holds(afterRows, ids[0]) {
		t.Fatalf("a message delivered before the window's start is outside it; rows=%+v", afterRows)
	}
	if !holds(afterRows, ids[2]) {
		t.Fatalf("a message delivered after the window's start belongs in it; rows=%+v", afterRows)
	}

	beforeRows := list("delivered_before=t2", persistence.MessageListFilter{DeliveredBefore: &boundary})
	if !holds(beforeRows, ids[1]) {
		t.Fatalf("a message delivered exactly at the window's end belongs in the window; rows=%+v", beforeRows)
	}
	if !holds(beforeRows, ids[0]) {
		t.Fatalf("a message delivered before the window's end belongs in it; rows=%+v", beforeRows)
	}
	if holds(beforeRows, ids[2]) {
		t.Fatalf("a message delivered after the window's end is outside it; rows=%+v", beforeRows)
	}

	windowRows := list("delivered_after=t2+before=t2", persistence.MessageListFilter{
		DeliveredAfter:  &boundary,
		DeliveredBefore: &boundary,
	})
	if len(windowRows) != 1 || windowRows[0].ID != ids[1] {
		t.Fatalf("a one-instant window holds exactly the message delivered at that instant; rows=%+v", windowRows)
	}

	unwindowed := list("no window", persistence.MessageListFilter{})
	if !holds(unwindowed, pendingID) {
		t.Fatalf("an unwindowed read lists an undelivered message; rows=%+v", unwindowed)
	}
	if holds(afterRows, pendingID) || holds(beforeRows, pendingID) {
		t.Fatalf("an undelivered message has no delivery instant. It falls outside every delivery window")
	}
	pending := true
	pendingRows := list("pending", persistence.MessageListFilter{Pending: &pending})
	if !holds(pendingRows, pendingID) {
		t.Fatalf("a caller reaches an undelivered message through the pending filter; rows=%+v", pendingRows)
	}
}

func testMessagesListCursorPagination(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	messages := store.Messages()

	const total = 7
	ids := make([]shared.UUID, total)
	base := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		ids[i] = shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return messages.Insert(ctx, persistence.EnqueueMessageRequest{
				ID:         ids[i],
				InstanceID: fix.InstanceID,
				Type:       "ping/recheck",
				Sender:     "operator",
				SenderKind: "operator",
				ReceivedAt: base.Add(time.Duration(i) * time.Minute),
			}, tx)
		}); err != nil {
			t.Fatalf("Messages.Insert[%d]: %v", i, err)
		}
	}

	var seen []shared.UUID
	pag := persistence.ListPagination{Limit: 3}
	for pages := 0; pages < total*2; pages++ {
		page, err := messages.List(ctx, persistence.MessageListFilter{InstanceID: &fix.InstanceID}, pag)
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}
		for _, r := range page.Rows {
			if r.ID == fix.MessageID {
				continue
			}
			seen = append(seen, r.ID)
		}
		if page.NextCursor == "" {
			break
		}
		pag.Cursor = page.NextCursor
	}

	if len(seen) != total {
		t.Fatalf("cursor pagination collected %d messages across pages, want %d "+
			"(next_cursor must actually page past the first window instead of re-serving it or stopping short)", len(seen), total)
	}
	seenSet := make(map[shared.UUID]bool, len(seen))
	for _, id := range seen {
		if seenSet[id] {
			t.Fatalf("message %s returned more than once across pages; cursor pagination must not duplicate rows", id)
		}
		seenSet[id] = true
	}
	for _, id := range ids {
		if !seenSet[id] {
			t.Fatalf("message %s never appeared across any page; cursor pagination must not drop rows", id)
		}
	}
}

func testMessagesListFiltersByType(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	insert := func(msgType string) shared.UUID {
		t.Helper()
		id := shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
				ID:         id,
				InstanceID: fix.InstanceID,
				Type:       msgType,
				Sender:     "fixture-sender",
				SenderKind: "operator",
				ReceivedAt: time.Now().UTC(),
			}, tx)
		}); err != nil {
			t.Fatalf("Messages.Insert(type=%q): %v", msgType, err)
		}
		return id
	}

	emptyWakeMsg := insert("")
	typedMsg := insert("fixture/typed")

	list := func(f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		res, err := store.Messages().List(ctx, f, persistence.ListPagination{Limit: 50})
		if err != nil {
			t.Fatalf("Messages.List: %v", err)
		}
		return res.Rows
	}

	emptyType := ""
	gotEmpty := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Type: &emptyType})
	if len(gotEmpty) != 1 || gotEmpty[0].ID != emptyWakeMsg {
		t.Fatalf("Type(\"\") = %v, want exactly [%s]", gotEmpty, emptyWakeMsg)
	}
	if !gotEmpty[0].IsEmptyWake() {
		t.Fatalf("Type(\"\") row %v, want IsEmptyWake() true", gotEmpty[0])
	}

	typedType := "fixture/typed"
	gotTyped := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Type: &typedType})
	if len(gotTyped) != 1 || gotTyped[0].ID != typedMsg {
		t.Fatalf("Type(fixture/typed) = %v, want exactly [%s]", gotTyped, typedMsg)
	}

	gotUnfiltered := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID})
	if len(gotUnfiltered) != 3 {
		t.Fatalf("Type(nil) = %d rows, want 3 (no filter applied: 2 inserted here + 1 fixture message)", len(gotUnfiltered))
	}
}

func testMessagesListPendingForInstanceReturnsAllPending(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	msgIDs := make([]shared.UUID, 3)
	for i := range msgIDs {
		msgIDs[i] = shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
				ID:         msgIDs[i],
				InstanceID: fix.InstanceID,
				Type:       "fixture/message",
				Sender:     "operator",
				SenderKind: "operator",
				ReceivedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			}, tx)
		}); err != nil {
			t.Fatalf("Messages.Insert(%d): %v", i, err)
		}
	}

	var pending []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Messages().ListPendingForInstance(ctx, fix.InstanceID, tx)
		pending = r
		return err
	}); err != nil {
		t.Fatalf("ListPendingForInstance: %v", err)
	}
	if len(pending) < len(msgIDs) {
		t.Fatalf("ListPendingForInstance returned %d rows, want at least %d (a List method must not silently truncate)",
			len(pending), len(msgIDs))
	}
	got := map[shared.UUID]bool{}
	for _, r := range pending {
		got[r.ID] = true
	}
	for _, id := range msgIDs {
		if !got[id] {
			t.Fatalf("ListPendingForInstance missing seeded message %s: got %v", id, pending)
		}
	}
}
