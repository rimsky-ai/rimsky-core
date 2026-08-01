// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"net/http"
	"strconv"
)

const parseLimitMax = resourceReadMaxLimit

func parseLimit(req *http.Request, dflt int) int {
	s := req.URL.Query().Get("limit")
	if s == "" {
		return dflt
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return dflt
	}
	if n > parseLimitMax {
		return parseLimitMax
	}
	return n
}
