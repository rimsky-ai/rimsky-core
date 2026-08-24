// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
)

const sqliteMaxLengthBytes = 1_000_000_000

// @decision: attribute-bytes-in-the-row
const MaxValueBytes = sqliteMaxLengthBytes

var ErrValueTooLarge = errors.New("persistence: value exceeds the database engine's per-value cap")

// @decision: attribute-bytes-in-the-row
func CheckValueSize(op string, nodeRunID fmt.Stringer, valueName string, size int) error {
	if size <= MaxValueBytes {
		return nil
	}
	return fmt.Errorf("%s: %w: node run %s: %s is %d bytes, cap is %d bytes",
		op, ErrValueTooLarge, nodeRunID, valueName, size, MaxValueBytes)
}

// @decision: attribute-bytes-in-the-row
func MergeAttributeBag(current []byte, delta map[string]any) ([]byte, error) {
	merged := map[string]any{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &merged); err != nil {
			return nil, fmt.Errorf("unmarshal current bag: %w", err)
		}
	}
	for k, v := range delta {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged bag: %w", err)
	}
	return out, nil
}
