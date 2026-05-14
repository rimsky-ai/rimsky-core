// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// client_errors.go — APIError carries non-2xx control-api responses.
package cli

import (
	"errors"
	"fmt"
	"net/http"
)

// APIError is a non-2xx response from the control-api. The decoded JSON
// body (if any) is stored verbatim; callers can read structured fields
// like "error", "validation_errors", or "details" from Body.
type APIError struct {
	Status int
	URL    string
	Method string
	Body   map[string]any
}

// Error returns a human-readable representation. Includes the status,
// the method and URL, and the body's "error" field if present.
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

// Message returns just the body's "error" field if present, falling
// back to the standard HTTP status text.
func (e *APIError) Message() string {
	if e.Body != nil {
		if v, ok := e.Body["error"].(string); ok && v != "" {
			return v
		}
	}
	return http.StatusText(e.Status)
}

// IsNotFound reports whether err is an APIError with status 404.
func IsNotFound(err error) bool { return statusEquals(err, http.StatusNotFound) }

// IsConflict reports whether err is an APIError with status 409.
func IsConflict(err error) bool { return statusEquals(err, http.StatusConflict) }

// IsBadRequest reports whether err is an APIError with status 400.
func IsBadRequest(err error) bool { return statusEquals(err, http.StatusBadRequest) }

func statusEquals(err error, status int) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == status
	}
	return false
}
