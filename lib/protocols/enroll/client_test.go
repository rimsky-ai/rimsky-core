// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package enroll

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEnroll_Success(t *testing.T) {
	notAfter := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	var gotAuth, gotPath, gotLabel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var body request
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotLabel = body.Label
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{
			CertPEM:   "cert",
			KeyPEM:    "key",
			CARootPEM: "root",
			NotAfter:  notAfter,
		})
	}))
	defer srv.Close()

	out, err := Enroll(context.Background(), srv.Client(), srv.URL, "secret-key", "scheduler-1")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", gotAuth)
	}
	if gotPath != Path {
		t.Fatalf("path = %q, want %q", gotPath, Path)
	}
	if gotLabel != "scheduler-1" {
		t.Fatalf("label = %q, want scheduler-1", gotLabel)
	}
	if out.CertPEM != "cert" || out.KeyPEM != "key" || out.CARootPEM != "root" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if !out.NotAfter.Equal(notAfter) {
		t.Fatalf("NotAfter = %s, want %s", out.NotAfter, notAfter)
	}
}

func TestEnroll_TrimsTrailingSlashOnBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(Response{CertPEM: "c", KeyPEM: "k", CARootPEM: "r"})
	}))
	defer srv.Close()

	if _, err := Enroll(context.Background(), srv.Client(), srv.URL+"/", "k", ""); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if gotPath != Path {
		t.Fatalf("path = %q, want %q (no double slash)", gotPath, Path)
	}
}

func TestEnroll_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"permission denied"}`))
	}))
	defer srv.Close()

	_, err := Enroll(context.Background(), srv.Client(), srv.URL, "k", "")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v, want it to mention status 403", err)
	}
}

func TestEnroll_IncompleteResponseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Response{CertPEM: "c"})
	}))
	defer srv.Close()

	if _, err := Enroll(context.Background(), srv.Client(), srv.URL, "k", ""); err == nil {
		t.Fatal("expected error when key_pem / ca_root_pem missing")
	}
}

func TestEnroll_RequiresAPIKey(t *testing.T) {
	if _, err := Enroll(context.Background(), http.DefaultClient, "http://example", "", ""); err == nil {
		t.Fatal("expected error when api key empty")
	}
}
