// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestClaimTerminalRecordCreation(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	ctx := context.Background()
	rec := runtime.ClaimTerminalRecord{
		ClaimHandleID:      shared.UUID(uuid.New()),
		NodeRunID:          shared.UUID(uuid.New()),
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            shared.UUID(uuid.New()),
		ProducerName:       "stub",
		ClaimScopeDataHash: runtime.HashBytes([]byte(`{"partition_key":"a"}`)),
		VersionID:          "v1",
		Outcome:            persistence.LineageOutcomeCommitted,
		ProducerMetadata:   map[string]any{"row_count": float64(1)},
	}
	if err := runtime.WriteClaimTerminalLineage(ctx, lt, shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec, nil); err != nil {
		t.Fatalf("WriteClaimTerminalLineage: %v", err)
	}
	if len(lt.rows) != 1 {
		t.Fatalf("expected 1 lineage row, got %d", len(lt.rows))
	}
	if lt.rows[0].RecordKind != persistence.LineageRecordKindClaimTerminal {
		t.Errorf("record_kind: got %s want claim_terminal", lt.rows[0].RecordKind)
	}
	if lt.rows[0].Outcome != persistence.LineageOutcomeCommitted {
		t.Errorf("outcome: got %s want committed", lt.rows[0].Outcome)
	}
	var decoded runtime.ClaimTerminalRecord
	if err := json.Unmarshal(lt.rows[0].Record, &decoded); err != nil {
		t.Fatalf("payload not JSON-decodable: %v", err)
	}
	if decoded.VersionID != "v1" || decoded.ProducerName != "stub" {
		t.Errorf("payload roundtrip mismatch: %+v", decoded)
	}
	if decoded.Outcome != persistence.LineageOutcomeCommitted {
		t.Errorf("payload outcome roundtrip: got %s", decoded.Outcome)
	}
}

func TestClaimTerminalRecordCreation_HashBytesIsStable(t *testing.T) {
	t.Parallel()
	got1 := runtime.HashBytes([]byte(`{"k":"v"}`))
	got2 := runtime.HashBytes([]byte(`{"k":"v"}`))
	if got1 != got2 {
		t.Errorf("HashBytes drift: %q vs %q", got1, got2)
	}
	if runtime.HashBytes(nil) != "" {
		t.Errorf("HashBytes(nil) should be empty, got %q", runtime.HashBytes(nil))
	}
}
