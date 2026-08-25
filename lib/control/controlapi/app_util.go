// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const parseLimitMax = resourceReadMaxLimit

var errInvalidLimit = errors.New("limit must be a positive integer")

func parseLimit(req *http.Request, dflt int) (int, error) {
	s := req.URL.Query().Get("limit")
	if s == "" {
		return dflt, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, errInvalidLimit
	}
	if n > parseLimitMax {
		return parseLimitMax, nil
	}
	return n, nil
}

func encodeSeqCursor(seq int64) string {
	return persistence.EncodeKeyCursor(strconv.FormatInt(seq, 10))
}

func decodeSeqCursor(cursor string) (int64, error) {
	raw, err := persistence.DecodeKeyCursor(cursor)
	if err != nil {
		return 0, persistence.ErrInvalidCursor
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0, persistence.ErrInvalidCursor
	}
	return seq, nil
}
