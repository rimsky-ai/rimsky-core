// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func capabilitiesWithSchema(t *testing.T, schema map[string]any) map[string][]byte {
	t.Helper()
	schemaStruct, err := structpb.NewStruct(schema)
	if err != nil {
		t.Fatalf("build schema struct: %v", err)
	}
	schemaBytes, err := schemaStruct.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal schema json: %v", err)
	}
	caps := &genv1.ObservabilityCapabilities{ExpectedAttributesSchema: schemaBytes}
	raw, err := proto.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	return map[string][]byte{protocolExecutor: raw}
}

func TestExecuteContractMismatchOnSchemaViolation(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setCapabilities(capabilitiesWithSchema(t, map[string]any{
		"type":     "object",
		"required": []any{"required_field"},
		"properties": map[string]any{
			"required_field": map[string]any{"type": "string"},
		},
	}))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()

	attrs, err := structpb.NewStruct(map[string]any{"other_field": "x"})
	if err != nil {
		t.Fatalf("build attrs: %v", err)
	}
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId: "inst-1",
		Attributes: attrs,
	})
	if got := terminalErrorClass(outcome); got != errClassContractMismatch {
		t.Fatalf("expected %s, got %q (outcome=%+v)", errClassContractMismatch, got, outcome)
	}
}

func TestExecuteContractSatisfiedDispatches(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setCapabilities(capabilitiesWithSchema(t, map[string]any{
		"type":     "object",
		"required": []any{"required_field"},
		"properties": map[string]any{
			"required_field": map[string]any{"type": "string"},
		},
	}))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()

	attrs, err := structpb.NewStruct(map[string]any{"required_field": "present"})
	if err != nil {
		t.Fatalf("build attrs: %v", err)
	}
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId: "inst-1",
		Attributes: attrs,
	})
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Outcome{Success} when the schema is satisfied, got %T (err_class=%s)",
			outcome.GetOutcome(), terminalErrorClass(outcome))
	}
}

func TestExecuteNoSchemaSkipsValidation(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()

	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Outcome{Success} when the spawned binary advertises no schema, got %T", outcome.GetOutcome())
	}
}

func TestResolveAndSpawnCapturesSpawnAckCapabilities(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	want := capabilitiesWithSchema(t, map[string]any{"type": "object"})
	fa.setCapabilities(want)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	_ = collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})

	spawnID, ok := ts.state.lookupSpawnByRunScopeBinding("inst-1", "codegen")
	if !ok {
		t.Fatalf("expected a recorded spawn")
	}
	sp, ok := ts.state.lookupSpawn(spawnID)
	if !ok {
		t.Fatalf("expected spawn state to be recorded")
	}
	if len(sp.capabilities[protocolExecutor]) == 0 {
		t.Fatalf("resolveAndSpawn discarded SpawnAck.Capabilities instead of recording it; got %+v", sp.capabilities)
	}
}
