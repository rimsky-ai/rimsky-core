// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// app_util.go
package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

func jsonMarshalImpl(v any) ([]byte, error) { return json.Marshal(v) }

// errorsIs is a tiny indirection so app.go's writeError doesn't need to import
// "errors" directly (and the sibling files can reference it by the same name).
func errorsIs(err, target error) bool { return errors.Is(err, target) }

// parseLimit pulls ?limit=N from the request, defaulting to dflt if missing or
// un-parseable. Values <= 0 also fall back to dflt.
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
