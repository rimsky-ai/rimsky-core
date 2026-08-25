// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClient_ListInstanceMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/abc/messages" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "ping/recheck" {
			t.Errorf("type filter missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{
					"id":          "m1",
					"instance_id": "abc",
					"type":        "ping/recheck",
					"sender":      "operator",
					"sender_kind": "operator",
					"received_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.ListInstanceMessages(context.Background(), "abc", ListMessagesQuery{Type: "ping/recheck"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].ID != "m1" {
		t.Errorf("messages: %+v", resp.Messages)
	}
	if resp.Messages[0].Type != "ping/recheck" {
		t.Errorf("decoded type: got %q, want %q", resp.Messages[0].Type, "ping/recheck")
	}
}

func TestClient_GetMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/m1" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "m1",
			"instance_id": "abc",
			"type":        "ping/recheck",
			"sender":      "operator",
			"sender_kind": "operator",
			"received_at": time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	m, err := c.GetMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "m1" {
		t.Errorf("id: %s", m.ID)
	}
	if m.Type != "ping/recheck" {
		t.Errorf("decoded type: got %q, want %q", m.Type, "ping/recheck")
	}
}

func TestRunMessagesTail_RequiresInstance(t *testing.T) {
	if code := RunMessagesTail(context.Background(), []string{}); code != 2 {
		t.Errorf("exit code: %d", code)
	}
}

func TestRunMessagesTail_SameInstantMessagesBothEmitted(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{"id": "m1", "instance_id": "abc", "type": "ping", "sender": "operator", "sender_kind": "operator", "received_at": ts},
				{"id": "m2", "instance_id": "abc", "type": "ping", "sender": "operator", "sender_kind": "operator", "received_at": ts},
			},
		})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	var code int
	out := captureStdout(t, func() {
		code = RunMessagesTail(context.Background(), []string{"--endpoint", srv.URL, "--instance", uuid})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	if !strings.Contains(out, "m1") {
		t.Errorf("expected m1 in output, got: %q", out)
	}
	if !strings.Contains(out, "m2") {
		t.Errorf("two messages sharing the same received_at instant: m2 was dropped by the dedup boundary, got: %q", out)
	}
}

func TestRunMessagesShow_RendersFields(t *testing.T) {
	receivedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	deliveredAt := receivedAt.Add(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/m1" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "m1",
			"instance_id":  "abc",
			"type":         "ping/recheck",
			"sender":       "operator",
			"sender_kind":  "operator",
			"received_at":  receivedAt.Format(time.RFC3339),
			"delivered_at": deliveredAt.Format(time.RFC3339),
			"frame_id":     "frame-9",
			"cancelled":    true,
		})
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunMessagesShow(context.Background(), []string{"--endpoint", srv.URL, "m1"})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	for _, want := range []string{"m1", "abc", "ping/recheck", "frame-9", "cancelled:", deliveredAt.Format(time.RFC3339)} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got: %q", want, out)
		}
	}
}

func TestRunMessagesShow_WrongArgCount(t *testing.T) {
	if code := RunMessagesShow(context.Background(), nil); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if code := RunMessagesShow(context.Background(), []string{"a", "b"}); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestClient_CreateInstanceMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/v1/instances/abc/messages" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "test-key" {
			t.Errorf("Idempotency-Key: got %q, want %q", got, "test-key")
		}
		var body struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Type != "" {
			t.Errorf("type: got %q, want empty", body.Type)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"message_id": "m1"})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.CreateInstanceMessage(context.Background(), "abc", "test-key", CreateInstanceMessageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.MessageID != "m1" {
		t.Errorf("MessageID: %s", resp.MessageID)
	}
}

// @concept: message
func TestRunMessagesTail_PrintsEveryRowOfADescendingPage(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rows := []map[string]any{}
		for i := 3; i >= 1; i-- {
			rows = append(rows, map[string]any{
				"id": "m" + strconv.Itoa(i), "instance_id": "abc", "type": "ping",
				"sender": "operator", "sender_kind": "operator",
				"received_at": base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": rows})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	var code int
	out := captureStdout(t, func() {
		code = RunMessagesTail(context.Background(), []string{"--endpoint", srv.URL, "--instance", uuid})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if !strings.Contains(out, id) {
			t.Errorf("row %s missing: the tail filters each page against the watermark taken before the poll "+
				"and advances it only after the page is printed, so a newest-first page prints whole; got: %q", id, out)
		}
	}
}

// @concept: message
func TestAdvanceTailWatermark_KeepsPriorIDsWhenTheWatermarkDoesNotMove(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	prior := map[string]struct{}{"m1": {}}
	got, seen := advanceTailWatermark(at, prior, []MessageItem{{ID: "m2", ReceivedAt: at}})
	if !got.Equal(at) {
		t.Fatalf("watermark = %v, want %v", got, at)
	}
	if _, ok := seen["m1"]; !ok {
		t.Errorf("a page that adds a row at the same instant must keep the ids already seen there: %v", seen)
	}
	if _, ok := seen["m2"]; !ok {
		t.Errorf("the newly printed row at the watermark instant must join the seen set: %v", seen)
	}
}

// @concept: message
func TestAdvanceTailWatermark_ResetsIDsWhenTheWatermarkMoves(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	later := at.Add(time.Second)
	prior := map[string]struct{}{"m1": {}}
	got, seen := advanceTailWatermark(at, prior, []MessageItem{
		{ID: "m2", ReceivedAt: later},
		{ID: "m3", ReceivedAt: at},
	})
	if !got.Equal(later) {
		t.Fatalf("watermark = %v, want %v", got, later)
	}
	if len(seen) != 1 {
		t.Fatalf("seen = %v, want only the row at the new watermark", seen)
	}
	if _, ok := seen["m2"]; !ok {
		t.Errorf("the row at the new watermark must be the seen set: %v", seen)
	}
}

// @concept: message
func TestRunMessagesTailNarrowsToTheDeliveryWindow(t *testing.T) {
	const since = "2026-08-01T00:00:00Z"
	const until = "2026-08-02T00:00:00Z"
	var gotAfter, gotBefore string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("delivered_after")
		gotBefore = r.URL.Query().Get("delivered_before")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{}})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	if code := RunMessagesTail(context.Background(), []string{
		"--endpoint", srv.URL, "--instance", uuid, "--since", since, "--until", until,
	}); code != 0 {
		t.Fatalf("messages tail --since --until: exit %d", code)
	}
	if gotAfter != since {
		t.Errorf("delivered_after = %q, want %q: --since names the start of the delivery window", gotAfter, since)
	}
	if gotBefore != until {
		t.Errorf("delivered_before = %q, want %q: --until names the end of the delivery window", gotBefore, until)
	}
}

func TestRunMessagesTailReachesAnUndeliveredMessageWithPending(t *testing.T) {
	var gotPending string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPending = r.URL.Query().Get("pending")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{
					"id": "m-pending", "instance_id": "abc", "type": "ping/recheck",
					"sender": "operator", "sender_kind": "operator",
					"received_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
			"next_cursor": "",
		})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	out := captureStdout(t, func() {
		if code := RunMessagesTail(context.Background(), []string{
			"--endpoint", srv.URL, "--instance", uuid, "--pending",
		}); code != 0 {
			t.Fatalf("messages tail --pending: exit %d", code)
		}
	})
	if gotPending != "true" {
		t.Errorf("pending = %q, want %q: --pending is how a caller reaches an undelivered message", gotPending, "true")
	}
	if !strings.Contains(out, "m-pending") {
		t.Errorf("the undelivered message did not reach stdout: %q", out)
	}
}

func TestRunMessagesTailRefusesPendingWithADeliveryWindow(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{}})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	for _, window := range [][]string{{"--since", "2026-08-01T00:00:00Z"}, {"--until", "2026-08-02T00:00:00Z"}} {
		args := append([]string{"--endpoint", srv.URL, "--instance", uuid, "--pending"}, window...)
		if code := RunMessagesTail(context.Background(), args); code == 0 {
			t.Fatalf("messages tail %v: exit 0; --pending and a delivery window select disjoint sets", window)
		}
	}
	if reached {
		t.Error("a refused verb still reached the deployment")
	}
}

func TestRunMessagesTailWalksTheWholeWindowAcrossPages(t *testing.T) {
	pages := map[string][]map[string]any{
		"": {
			{"id": "m1", "instance_id": "abc", "type": "t", "sender": "s", "sender_kind": "operator",
				"received_at": "2026-08-01T00:00:03Z"},
		},
		"c1": {
			{"id": "m2", "instance_id": "abc", "type": "t", "sender": "s", "sender_kind": "operator",
				"received_at": "2026-08-01T00:00:02Z"},
		},
		"c2": {
			{"id": "m3", "instance_id": "abc", "type": "t", "sender": "s", "sender_kind": "operator",
				"received_at": "2026-08-01T00:00:01Z"},
		},
	}
	next := map[string]string{"": "c1", "c1": "c2", "c2": ""}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages":    pages[cursor],
			"next_cursor": next[cursor],
		})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	out := captureStdout(t, func() {
		if code := RunMessagesTail(context.Background(), []string{
			"--endpoint", srv.URL, "--instance", uuid, "--since", "2026-08-01T00:00:00Z",
		}); code != 0 {
			t.Fatalf("messages tail --since: non-zero exit")
		}
	})
	for _, id := range []string{"m1", "m2", "m3"} {
		if !strings.Contains(out, id) {
			t.Errorf("a windowed read stopped at the first page: %q is missing from %q", id, out)
		}
	}
}

func TestRunMessagesTailWithoutAWindowReadsOneBoundedPage(t *testing.T) {
	var requests int
	var gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{"id": "m1", "instance_id": "abc", "type": "t", "sender": "s", "sender_kind": "operator",
					"received_at": "2026-08-01T00:00:03Z"},
			},
			"next_cursor": "c1",
		})
	}))
	defer srv.Close()

	const uuid = "5cb9362f-1111-2222-3333-444455556666"
	var out string
	notice := captureStderr(t, func() {
		out = captureStdout(t, func() {
			if code := RunMessagesTail(context.Background(), []string{
				"--endpoint", srv.URL, "--instance", uuid,
			}); code != 0 {
				t.Fatalf("messages tail: non-zero exit")
			}
		})
	})
	if requests != 1 {
		t.Errorf("a bare tail issued %d requests; it reads one bounded page", requests)
	}
	if gotLimit != "100" {
		t.Errorf("limit=%q, want the tail's page size", gotLimit)
	}
	if !strings.Contains(out, "m1") {
		t.Errorf("the newest page is missing from %q", out)
	}
	if !strings.Contains(notice, "--since") {
		t.Errorf("messages tail truncated its read and named no window flags; stderr=%q", notice)
	}
	if strings.Contains(out, "--since") {
		t.Errorf("messages tail printed its notice among the rows on stdout; stdout=%q", out)
	}
}
