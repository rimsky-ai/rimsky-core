// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

func jsonMarshalImpl(v any) ([]byte, error) { return json.Marshal(v) }

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func parseLimit(req *http.Request, dflt int) int {
	s := req.URL.Query().Get("limit")
	if s == "" {
		return dflt
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return dflt
	}
	return n
}
