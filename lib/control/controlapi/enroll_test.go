// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type captureEvent struct {
	msg    string
	fields []any
}

type captureLogger struct {
	mu     *sync.Mutex
	events *[]captureEvent
}

func (c captureLogger) record(msg string, fields ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.events = append(*c.events, captureEvent{msg: msg, fields: append([]any(nil), fields...)})
}

func (c captureLogger) Debug(msg string, f ...any)  { c.record(msg, f...) }
func (c captureLogger) Info(msg string, f ...any)   { c.record(msg, f...) }
func (c captureLogger) Warn(msg string, f ...any)   { c.record(msg, f...) }
func (c captureLogger) Error(msg string, f ...any)  { c.record(msg, f...) }
func (c captureLogger) With(_ ...any) shared.Logger { return c }

func (c captureLogger) fieldFor(msg, key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range *c.events {
		if e.msg != msg {
			continue
		}
		for i := 0; i+1 < len(e.fields); i += 2 {
			if k, _ := e.fields[i].(string); k == key {
				return e.fields[i+1], true
			}
		}
	}
	return nil, false
}

type enrollHarness struct {
	srv    *httptest.Server
	tables persistence.Tables
	clock  shared.Clock
	logger captureLogger
}

func newEnrollHarness(t *testing.T, withEnroll, withEnrollClock bool) enrollHarness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clock := shared.NewControllableClock(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC))
	authState := &AuthState{
		Tables:   d.Tables(),
		Registry: BuildV1Registry(),
		Clock:    clock,
		Logger:   shared.SilentLogger{},
	}
	logger := captureLogger{mu: &sync.Mutex{}, events: &[]captureEvent{}}
	deps := AppDeps{
		Persist:        d.Tables(),
		AdvisoryLocker: d.AdvisoryLocker(),
		Clock:          clock,
		Logger:         logger,
		AuthState:      authState,
	}
	if withEnroll {
		ca, err := pki.GenerateCA(clock.Now())
		if err != nil {
			t.Fatalf("GenerateCA: %v", err)
		}
		deps.PeerAuth = "mtls"
		enrollClock := shared.Clock(nil)
		if withEnrollClock {
			enrollClock = clock
		}
		deps.Enroll = &EnrollDeps{CA: ca, LeafTTL: pki.LeafTTL, Clock: enrollClock}
	}
	srv := httptest.NewServer(NewApp(deps))
	t.Cleanup(srv.Close)
	return enrollHarness{srv: srv, tables: d.Tables(), clock: clock, logger: logger}
}

func (h enrollHarness) seedKey(t *testing.T, name string, actions ...string) (id uuid.UUID, plaintext string) {
	t.Helper()
	plaintext, hash, err := auth.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	grant := make(auth.Grant, 0, len(actions))
	for _, a := range actions {
		grant = append(grant, auth.GrantEntry{Action: a})
	}
	permsJSON, _ := json.Marshal(grant)
	id = uuid.New()
	err = h.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return h.tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          id,
			Name:        name,
			KeyHash:     hash[:],
			Permissions: permsJSON,
			CreatedAt:   h.clock.Now(),
		}, tx)
	})
	if err != nil {
		t.Fatalf("seed key %q: %v", name, err)
	}
	return id, plaintext
}

func (h enrollHarness) post(t *testing.T, path, bearer string) (int, map[string]any) {
	return h.postBody(t, path, bearer, nil)
}

func (h enrollHarness) postBody(t *testing.T, path, bearer string, body []byte) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest("POST", h.srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func TestEnrollReturnsCertWithCallerKeyIDInSAN(t *testing.T) {
	h := newEnrollHarness(t, true, true)
	id, plaintext := h.seedKey(t, "enroller", "service:enroll")

	status, body := h.post(t, "/v1/enroll", plaintext)
	if status != http.StatusOK {
		t.Fatalf("enroll status: got %d body=%v", status, body)
	}
	certPEM, _ := body["cert_pem"].(string)
	if certPEM == "" {
		t.Fatalf("response missing cert_pem: %v", body)
	}
	if body["key_pem"] == "" || body["ca_root_pem"] == "" || body["not_after"] == nil {
		t.Fatalf("response missing fields: %v", body)
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatalf("cert_pem did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	wantURI := pki.SpiffeURI(id.String())
	found := false
	for _, u := range cert.URIs {
		if u.String() == wantURI {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("issued cert SAN URIs %v do not include %q", cert.URIs, wantURI)
	}
}

func TestEnrollLogsServiceLabel(t *testing.T) {
	h := newEnrollHarness(t, true, true)
	id, plaintext := h.seedKey(t, "enroller", "service:enroll")

	status, body := h.postBody(t, "/v1/enroll", plaintext, []byte(`{"label":"sensor-webhook"}`))
	if status != http.StatusOK {
		t.Fatalf("enroll status: got %d body=%v", status, body)
	}

	gotLabel, ok := h.logger.fieldFor("service enrolled", "label")
	if !ok {
		t.Fatalf("enrollment did not emit a 'service enrolled' log line carrying the client label — the wire label is dead again")
	}
	if gotLabel != "sensor-webhook" {
		t.Fatalf("enrollment logged label = %v, want %q", gotLabel, "sensor-webhook")
	}
	gotPrincipal, ok := h.logger.fieldFor("service enrolled", "principal")
	if !ok || gotPrincipal != id.String() {
		t.Fatalf("enrollment logged principal = %v (present=%v), want authenticated key id %q", gotPrincipal, ok, id.String())
	}
}

func TestEnrollForbiddenWithoutPermission(t *testing.T) {
	h := newEnrollHarness(t, true, true)
	_, plaintext := h.seedKey(t, "reader", "instance:read")
	status, _ := h.post(t, "/v1/enroll", plaintext)
	if status != http.StatusForbidden {
		t.Fatalf("enroll without service:enroll must be 403, got %d", status)
	}
}

func TestEnrollRouteAbsentWhenPeerAuthNone(t *testing.T) {
	h := newEnrollHarness(t, false, false)
	_, plaintext := h.seedKey(t, "enroller", "service:enroll")
	status, _ := h.post(t, "/v1/enroll", plaintext)
	if status != http.StatusNotFound {
		t.Fatalf("enroll route must be absent (404) when no CA/enroll configured, got %d", status)
	}
}

func TestEnrollWithNilClockDefaultsToSystemClock(t *testing.T) {
	h := newEnrollHarness(t, true, false)
	id, plaintext := h.seedKey(t, "enroller", "service:enroll")

	status, body := h.post(t, "/v1/enroll", plaintext)
	if status != http.StatusOK {
		t.Fatalf("enroll with a nil-configured clock must not panic and must issue a cert: got %d body=%v", status, body)
	}
	certPEM, _ := body["cert_pem"].(string)
	if certPEM == "" {
		t.Fatalf("response missing cert_pem: %v", body)
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatalf("cert_pem did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	wantURI := pki.SpiffeURI(id.String())
	found := false
	for _, u := range cert.URIs {
		if u.String() == wantURI {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("issued cert SAN URIs %v do not include %q", cert.URIs, wantURI)
	}
}
