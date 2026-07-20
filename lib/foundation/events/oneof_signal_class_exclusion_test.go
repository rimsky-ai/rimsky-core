// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package events_test

import (
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestEventPayloadOneof_NeverCarriesASignalClassKind(t *testing.T) {
	desc := (&genv1.Event{}).ProtoReflect().Descriptor()
	oneof := desc.Oneofs().ByName("payload")
	if oneof == nil {
		t.Fatal("events.Event proto descriptor has no oneof named \"payload\" — has the wire shape changed?")
	}
	fields := oneof.Fields()
	if fields.Len() == 0 {
		t.Fatal("events.Event's payload oneof has no cases")
	}

	allOperational := make(map[string]bool, len(events.AllOperationalKinds()))
	for _, k := range events.AllOperationalKinds() {
		allOperational[k] = true
	}

	for i := 0; i < fields.Len(); i++ {
		caseName := string(fields.Get(i).Name())
		if strings.Contains(caseName, "/") {
			t.Errorf("payload oneof case %q looks signal-class-shaped (contains \"/\"); "+
				"signal-class events (terminal/, transient/, attribute/, event/, message/) must "+
				"never be typed oneof members — they always go through payload_raw", caseName)
		}
		if !allOperational[caseName] {
			t.Errorf("payload oneof case %q has no matching entry in events.AllOperationalKinds(); "+
				"every typed oneof payload must correspond to a declared OperationalKind wire form, "+
				"or a divergent inline construction outside the OperationalKind taxonomy would go uncaught", caseName)
		}
	}
}
