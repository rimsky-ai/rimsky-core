// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSetup_ReturnsLogger_AndInstallsDefault confirms Setup returns
// a logger and installs it as the slog default.
func TestSetup_ReturnsLogger_AndInstallsDefault(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	got := Setup(slog.LevelInfo)
	if got == nil {
		t.Fatal("Setup returned nil logger")
	}
	if slog.Default() != got {
		t.Fatal("Setup did not install the logger as default")
	}
}

// TestHealthHandler_Get_Returns200 confirms the happy path.
func TestHealthHandler_Get_Returns200(t *testing.T) {
	h := HealthHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("body=%q does not contain status:ok", body)
	}
}

// TestHealthHandler_NonGet_Returns405 confirms POST is rejected.
func TestHealthHandler_NonGet_Returns405(t *testing.T) {
	h := HealthHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", rr.Code)
	}
}

// TestHealthHandler_ReadyErr_Returns503 confirms the readiness path
// surfaces the error.
func TestHealthHandler_ReadyErr_Returns503(t *testing.T) {
	h := HealthHandler(func() error { return errors.New("dependency down") })
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "dependency down") {
		t.Fatalf("body=%q does not include error", body)
	}
}

// TestDSNFromEnv_Unset returns the empty string + nil error.
func TestDSNFromEnv_Unset(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DSN_UNSET", "")
	dsn, err := DSNFromEnv("RIMSKY_TEST_DSN_UNSET")
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if dsn != "" {
		t.Fatalf("dsn=%q, want empty", dsn)
	}
}

// TestDSNFromEnv_Set returns the value trimmed.
func TestDSNFromEnv_Set(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DSN_SET", "  postgres://u:p@host/db\n")
	dsn, err := DSNFromEnv("RIMSKY_TEST_DSN_SET")
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if dsn != "postgres://u:p@host/db" {
		t.Fatalf("dsn=%q, want trimmed value", dsn)
	}
}

// TestDSNFromEnv_WhitespaceOnly returns an error mentioning the env
// var name so operators can find the misconfiguration.
func TestDSNFromEnv_WhitespaceOnly(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DSN_WS", "   \t\n")
	_, err := DSNFromEnv("RIMSKY_TEST_DSN_WS")
	if err == nil {
		t.Fatal("err=nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "RIMSKY_TEST_DSN_WS") {
		t.Fatalf("err=%q, want env var name", err.Error())
	}
}
