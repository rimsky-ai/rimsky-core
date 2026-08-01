// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"errors"
	"fmt"
	"net/http"
)

type APIError struct {
	Status int
	URL    string
	Method string
	Body   map[string]any
}

func (e *APIError) Error() string {
	msg := ""
	if e.Body != nil {
		if v, ok := e.Body["error"].(string); ok {
			msg = v
		}
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("control-api %s %s: %d %s", e.Method, e.URL, e.Status, msg)
}

func (e *APIError) Message() string {
	if e.Body != nil {
		if v, ok := e.Body["error"].(string); ok && v != "" {
			return v
		}
	}
	return http.StatusText(e.Status)
}

func IsNotFound(err error) bool { return statusEquals(err, http.StatusNotFound) }

func IsConflict(err error) bool { return statusEquals(err, http.StatusConflict) }

func IsBadRequest(err error) bool { return statusEquals(err, http.StatusBadRequest) }

func statusEquals(err error, status int) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == status
	}
	return false
}
