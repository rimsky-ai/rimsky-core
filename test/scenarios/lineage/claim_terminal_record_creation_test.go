// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N7 scenario — claim_terminal_record_creation.
//
// At every ClaimProducer terminal (Commit / Abandon / force-cancelled),
// the supervisor's terminal-decision engine calls
// runtime.WriteClaimTerminalLineage with the per-claim payload bytes
// plus the per-row `outcome` column populated from the
// LineageOutcome* constants.
package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/runtime"
)

func TestClaimTerminalRecordCreation(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	ctx := context.Background()
	rec := runtime.ClaimTerminalRecord{
		ClaimHandleID:      shared.UUID(uuid.New()),
		RunID:              shared.UUID(uuid.New()),
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            shared.UUID(uuid.New()),
		ProducerName:       "stub",
		ClaimScopeDataHash: runtime.HashBytes([]byte(`{"partition_key":"a"}`)),
		VersionID:          "v1",
		Outcome:            persistence.LineageOutcomeCommitted,
		ProducerMetadata:   map[string]any{"row_count": float64(1)},
	}
	if err := runtime.WriteClaimTerminalLineage(ctx, nil, lt,
		shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec); err != nil {
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
	// Pin the hash convention so downstream consumers can rely on it.
	got1 := runtime.HashBytes([]byte(`{"k":"v"}`))
	got2 := runtime.HashBytes([]byte(`{"k":"v"}`))
	if got1 != got2 {
		t.Errorf("HashBytes drift: %q vs %q", got1, got2)
	}
	if runtime.HashBytes(nil) != "" {
		t.Errorf("HashBytes(nil) should be empty, got %q", runtime.HashBytes(nil))
	}
}
