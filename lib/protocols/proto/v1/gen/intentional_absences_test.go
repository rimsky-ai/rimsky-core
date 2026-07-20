// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import "testing"

func TestExecuteRequest_ReservesUserdataAndResumeContext(t *testing.T) {
	desc := (&ExecuteRequest{}).ProtoReflect().Descriptor()
	assertFieldsReserved(t, "ExecuteRequest", desc, []reservedFieldCase{
		{4, "userdata"},
		{13, "resume_context"},
	})
}

func TestExecutorContext_ReservesUserdata(t *testing.T) {
	desc := (&ExecutorContext{}).ProtoReflect().Descriptor()
	assertFieldsReserved(t, "ExecutorContext", desc, []reservedFieldCase{
		{2, "userdata"},
	})
}

func TestPublisherWireShapes_ReserveTargetNode(t *testing.T) {
	assertFieldsReserved(t, "SubscribeRequest", (&SubscribeRequest{}).ProtoReflect().Descriptor(),
		[]reservedFieldCase{{5, "target_node"}})
	assertFieldsReserved(t, "PublisherSubscriptionDescriptor", (&PublisherSubscriptionDescriptor{}).ProtoReflect().Descriptor(),
		[]reservedFieldCase{{5, "target_node"}})
}
