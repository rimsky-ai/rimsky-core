// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrInvalidCursor = errors.New("invalid cursor")

const sortableTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func SortableTimeKey(t time.Time) string {
	return t.UTC().Format(sortableTimeLayout)
}

type ListPagination struct {
	Limit  int
	Cursor string
}

type PaginatedListResult[T any] struct {
	Rows       []T
	NextCursor string
}

func EncodeCursor(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ErrInvalidCursor
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return ErrInvalidCursor
	}
	return nil
}

type keyCursor struct {
	K string `json:"k"`
}

func EncodeKeyCursor(key string) string {
	return EncodeCursor(keyCursor{K: key})
}

func DecodeKeyCursor(s string) (string, error) {
	var c keyCursor
	if err := DecodeCursor(s, &c); err != nil {
		return "", err
	}
	return c.K, nil
}

func PageByKey[T any](rows []T, cursor string, limit int, key func(T) string) ([]T, string, error) {
	sorted := make([]T, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool { return key(sorted[i]) < key(sorted[j]) })
	if cursor != "" {
		after, err := DecodeKeyCursor(cursor)
		if err != nil {
			return nil, "", ErrInvalidCursor
		}
		start := 0
		for start < len(sorted) && key(sorted[start]) <= after {
			start++
		}
		sorted = sorted[start:]
	}
	if len(sorted) > limit {
		sorted = sorted[:limit]
		return sorted, EncodeKeyCursor(key(sorted[len(sorted)-1])), nil
	}
	return sorted, "", nil
}

type claimHandleCursor struct {
	C time.Time   `json:"c"`
	I shared.UUID `json:"i"`
}

func EncodeClaimHandleCursor(claimed time.Time, id shared.UUID) string {
	return EncodeCursor(claimHandleCursor{C: claimed, I: id})
}

func DecodeClaimHandleCursor(s string) (time.Time, shared.UUID, error) {
	var c claimHandleCursor
	if err := DecodeCursor(s, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.C, c.I, nil
}

type eventCursor struct {
	O time.Time `json:"o"`
	I int64     `json:"i"`
}

func EncodeEventCursor(occurred time.Time, id int64) string {
	return EncodeCursor(eventCursor{O: occurred, I: id})
}

func DecodeEventCursor(s string) (time.Time, int64, error) {
	var c eventCursor
	if err := DecodeCursor(s, &c); err != nil {
		return time.Time{}, 0, err
	}
	return c.O, c.I, nil
}
