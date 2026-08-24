// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	sqliteMaxLengthUnderTest  = 1_000_000_000
	postgresFieldCapUnderTest = 1 << 30
)

// @decision: attribute-bytes-in-the-row
func TestCheckValueSizeAdmitsEveryValueTheEngineAccepts(t *testing.T) {
	t.Parallel()
	runID := shared.UUID(uuid.New())
	for _, size := range []int{0, 1, 65536, 512 * 1024, MaxValueBytes} {
		if err := CheckValueSize("node_attributes.Upsert", runID, "attribute bag", size); err != nil {
			t.Fatalf("a %d-byte value must commit with its row, got %v", size, err)
		}
	}
}

// @decision: attribute-bytes-in-the-row
func TestCheckValueSizeRefusesBeforeEitherEngineRefusesAndNamesWhatItRefused(t *testing.T) {
	t.Parallel()
	runID := shared.UUID(uuid.New())
	overCap := map[string]int{
		"a value past SQLite's per-value cap":    sqliteMaxLengthUnderTest + 1,
		"a value past Postgres's per-field cap":  postgresFieldCapUnderTest + 1,
		"a value far past both engines' ceiling": 4 * postgresFieldCapUnderTest,
	}
	for what, size := range overCap {
		err := CheckValueSize("node_attributes.Upsert", runID, "attribute bag", size)
		if err == nil {
			t.Fatalf("%s must fail at the write with rimsky's own error, not the driver's", what)
		}
		if !errors.Is(err, ErrValueTooLarge) {
			t.Fatalf("%s: error %v does not identify itself as an over-cap write", what, err)
		}
		msg := err.Error()
		for _, want := range []string{runID.String(), "attribute bag", strconv.Itoa(size)} {
			if !strings.Contains(msg, want) {
				t.Fatalf("%s: error %q does not name %q", what, msg, want)
			}
		}
	}
}
