// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"google.golang.org/protobuf/types/known/emptypb"
)

func advertisedBackends(t *testing.T, s *SensorService) []string {
	t.Helper()
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	var schema struct {
		Properties struct {
			Backend struct {
				Enum []string `json:"enum"`
			} `json:"backend"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(caps.SupportedKinds[0].ConfigSchema, &schema); err != nil {
		t.Fatalf("unmarshal config schema: %v", err)
	}
	return schema.Properties.Backend.Enum
}

// @decision: object-store-watching-model
func TestStockDeploymentAdvertisesOnlyTheFilesystemBackend(t *testing.T) {
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT", t.TempDir())
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND", "")

	s := NewSensorService("", noopLogger{})
	registerBackendsFromEnv(s)

	got := advertisedBackends(t, s)
	if len(got) != 1 || got[0] != "filesystem" {
		t.Errorf("stock deployment advertises %v; the in-memory store forgets its watch state on restart and must not be selectable without an explicit enable", got)
	}
}

// @decision: object-store-watching-model
func TestStockDeploymentRefusesASubscriptionOnTheMemoryBackend(t *testing.T) {
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT", t.TempDir())
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND", "")

	s := NewSensorService("", noopLogger{})
	registerBackendsFromEnv(s)

	raw, err := json.Marshal(map[string]any{"backend": "memory", "bucket": "b"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	_, err = s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "object-store",
		ResolvedConfig: raw, MessageType: "invalidate",
	})
	if err == nil {
		t.Fatal("subscribing on the memory backend succeeded on a stock deployment; it must be refused")
	}
	if !strings.Contains(err.Error(), "not serviceable") {
		t.Errorf("refusal should name the unserviceable backend, got: %v", err)
	}
}

// @decision: object-store-watching-model
func TestExplicitEnableRegistersTheMemoryBackend(t *testing.T) {
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT", t.TempDir())
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND", "1")

	s := NewSensorService("", noopLogger{})
	registerBackendsFromEnv(s)

	got := advertisedBackends(t, s)
	if len(got) != 2 || got[0] != "filesystem" || got[1] != "memory" {
		t.Errorf("with the memory backend enabled, advertised backends = %v; want filesystem and memory", got)
	}
}
