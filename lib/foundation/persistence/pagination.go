// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrInvalidCursor = errors.New("invalid cursor")

type ListPagination struct {
	Limit  int
	Cursor string
}

type PaginatedListResult[T any] struct {
	Rows       []T
	NextCursor string
}

type claimHandleCursor struct {
	C time.Time   `json:"c"`
	I shared.UUID `json:"i"`
}

func EncodeClaimHandleCursor(claimed time.Time, id shared.UUID) string {
	b, _ := json.Marshal(claimHandleCursor{C: claimed, I: id})
	return base64.StdEncoding.EncodeToString(b)
}

func DecodeClaimHandleCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c claimHandleCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.C, c.I, nil
}

type eventCursor struct {
	O time.Time `json:"o"`
	I int64     `json:"i"`
}

func EncodeEventCursor(occurred time.Time, id int64) string {
	c := eventCursor{O: occurred, I: id}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func DecodeEventCursor(s string) (time.Time, int64, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, err
	}
	var c eventCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, 0, err
	}
	return c.O, c.I, nil
}
