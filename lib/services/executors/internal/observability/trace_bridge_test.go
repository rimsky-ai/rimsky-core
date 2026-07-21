// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestMountTraceBridge_RejectsNonGET(t *testing.T) {
	mux := http.NewServeMux()
	MountTraceBridge(mux, NewStore())
	req := httptest.NewRequest(http.MethodPost, "/observability/v1/trace/d1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestMountTraceBridge_RejectsNestedDispatchID(t *testing.T) {
	mux := http.NewServeMux()
	MountTraceBridge(mux, NewStore())
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/trace/d1/extra/stream", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMountTraceBridge_GetTraceRoutesToJSON(t *testing.T) {
	s := NewStore()
	s.RegisterDispatch("d1")
	s.AppendEvent("d1", MakeEvent("e1", "", "log", "hello", genv1.Severity_INFO, nil))
	mux := http.NewServeMux()
	MountTraceBridge(mux, s)
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/trace/d1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("body = %q, want it to contain the appended event", rec.Body.String())
	}
}

func readSSEDataLines(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func TestHandleTraceStreamHTTP_UnknownDispatchIDSendsCompleteImmediately(t *testing.T) {
	s := NewStore()
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/trace/unknown/stream", nil)
	rec := httptest.NewRecorder()
	HandleTraceStreamHTTP(rec, req, s, "unknown")

	lines := readSSEDataLines(t, rec.Body.String())
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 SSE data line for an unknown dispatch_id, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "trace_complete") && !strings.Contains(lines[0], "complete") {
		t.Fatalf("expected a completion event, got %q", lines[0])
	}
}

func TestHandleTraceStreamHTTP_DrainsExistingEventsThenTerminal(t *testing.T) {
	s := NewStore()
	s.RegisterDispatch("d1")
	s.AppendEvent("d1", MakeEvent("e1", "", "log", "hello", genv1.Severity_INFO, nil))
	s.MarkTerminal("d1")

	req := httptest.NewRequest(http.MethodGet, "/observability/v1/trace/d1/stream", nil)
	rec := httptest.NewRecorder()
	HandleTraceStreamHTTP(rec, req, s, "d1")

	lines := readSSEDataLines(t, rec.Body.String())
	if len(lines) < 2 {
		t.Fatalf("expected the seeded event plus a terminal completion event, got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "hello") {
		t.Fatalf("first SSE line = %q, want it to contain the seeded event", lines[0])
	}
}
