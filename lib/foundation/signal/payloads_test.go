// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package signal

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestBuildTerminalSuccessSignal_PayloadCarriesEveryDeclaredField(t *testing.T) {
	sig := BuildTerminalSuccessSignal(true, map[string]any{"x": "y"}, "foo", []string{"t1"})
	if sig.Type != "terminal/success" {
		t.Fatalf("type = %q", sig.Type)
	}
	m := sig.Payload.Map()
	assertFieldsMatchDescriptor(t, m, descriptorOf(&genv1.TerminalSuccessSignalPayload{}))
	if m["changed"] != true {
		t.Fatalf("changed = %v", m["changed"])
	}
	delta, ok := m["attributes_delta"].(map[string]any)
	if !ok || delta["x"] != "y" {
		t.Fatalf("attributes_delta = %v", m["attributes_delta"])
	}
	if m["change_summary"] != "foo" {
		t.Fatalf("change_summary = %v", m["change_summary"])
	}
}

func TestBuildTerminalErrorSignal_PayloadCarriesEveryDeclaredField(t *testing.T) {
	sig := BuildTerminalErrorSignal("http/timeout", map[string]any{"status": 504}, 2, 2, map[string]any{"a": 1}, nil)
	if sig.Type != "terminal/error/http/timeout" {
		t.Fatalf("type = %q", sig.Type)
	}
	m := sig.Payload.Map()
	assertFieldsMatchDescriptor(t, m, descriptorOf(&genv1.TerminalErrorSignalPayload{}))
	if m["error_class"] != "http/timeout" {
		t.Fatalf("error_class = %v", m["error_class"])
	}
	if m["attempt"] != float64(2) {
		t.Fatalf("attempt = %v", m["attempt"])
	}
	errPayload, ok := m["error_payload"].(map[string]any)
	if !ok || errPayload["status"] != float64(504) {
		t.Fatalf("error_payload = %v", m["error_payload"])
	}
}

func TestPayloadSchemaForType(t *testing.T) {
	cases := []struct {
		path  TypePath
		want  string
		found bool
	}{
		{"terminal/success", "TerminalSuccessSignalPayload", true},
		{"terminal/error/http/timeout", "TerminalErrorSignalPayload", true},
		{"terminal/error/foo", "TerminalErrorSignalPayload", true},
		{"terminal/error/*", "TerminalErrorSignalPayload", true},
		{"transient/park", "TransientParkSignalPayload", true},
		{"transient/retry/3/agent/rate_limited", "TransientRetrySignalPayload", true},
		{"transient/retry/*", "TransientRetrySignalPayload", true},
		{"transient/await_async", "TransientAwaitAsyncSignalPayload", true},
		{"attribute/budget_cents/changed", "AttributeChangedSignalPayload", true},
		{"", "", false},
		{"transient/infra/whatever", "", false},
	}
	for _, c := range cases {
		got, ok := PayloadSchemaForType(c.path)
		if ok != c.found {
			t.Fatalf("PayloadSchemaForType(%q): ok=%v want=%v", c.path, ok, c.found)
		}
		if !ok {
			continue
		}
		if string(got.Name()) != c.want {
			t.Fatalf("PayloadSchemaForType(%q): got %s want %s", c.path, got.Name(), c.want)
		}
	}
}

func assertFieldsMatchDescriptor(t *testing.T, m map[string]any, d protoreflect.MessageDescriptor) {
	t.Helper()
	declared := map[string]struct{}{}
	fields := d.Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		declared[name] = struct{}{}
		if _, ok := m[name]; !ok {
			t.Fatalf("%s declares %q but the emitted payload has no such key", d.Name(), name)
		}
	}
	for k := range m {
		if _, ok := declared[k]; !ok {
			t.Fatalf("the emitted payload carries %q, which %s does not declare", k, d.Name())
		}
	}
}
