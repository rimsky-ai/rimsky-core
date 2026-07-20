// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type ListPagination struct {
	Limit  int
	Cursor string
}

type PaginatedListResult[T any] struct {
	Rows       []T
	NextCursor string
}

type lockHolderCursor struct {
	C time.Time   `json:"c"`
	I shared.UUID `json:"i"`
}

func EncodeLockHolderCursor(claimed time.Time, id shared.UUID) string {
	b, _ := json.Marshal(lockHolderCursor{C: claimed, I: id})
	return base64.StdEncoding.EncodeToString(b)
}

func DecodeLockHolderCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c lockHolderCursor
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
