// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
