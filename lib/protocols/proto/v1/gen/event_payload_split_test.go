// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import (
	"strings"
	"testing"
)

func TestEventPayloadOneof_ExcludesSignalClassKinds(t *testing.T) {
	msg := (&Event{}).ProtoReflect()
	oneofs := msg.Descriptor().Oneofs()

	var payloadOneofFieldCount int
	for i := 0; i < oneofs.Len(); i++ {
		oneof := oneofs.Get(i)
		if string(oneof.Name()) != "payload" {
			continue
		}
		fields := oneof.Fields()
		for j := 0; j < fields.Len(); j++ {
			f := fields.Get(j)
			payloadOneofFieldCount++
			msgDesc := f.Message()
			if msgDesc == nil {
				continue
			}
			typeName := string(msgDesc.Name())
			lower := strings.ToLower(typeName)
			if strings.Contains(lower, "terminal") || strings.Contains(lower, "transient") ||
				strings.Contains(lower, "signal") {
				t.Errorf("Event.payload oneof case %q (field %q) looks signal-class (terminal/transient/signal); "+
					"signal-class event kinds must stay free-form JSON via payload_raw, never gain a typed "+
					"oneof slot reserved for operational event kinds",
					typeName, f.Name())
			}
		}
	}
	if payloadOneofFieldCount == 0 {
		t.Fatalf("Event.payload oneof has no fields; the test fixture assumption (a populated operational " +
			"oneof) no longer holds")
	}
}
