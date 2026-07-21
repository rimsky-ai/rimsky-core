// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import "fmt"

const RootUnavailableClass = "fs/root_unavailable"

type ClassedError struct {
	Class string
	Err   error
}

func (e *ClassedError) Error() string {
	if e.Err == nil {
		return e.Class
	}
	return fmt.Sprintf("%s: %v", e.Class, e.Err)
}

func (e *ClassedError) Unwrap() error { return e.Err }

type ValidationError struct {
	Err error
}

func (e *ValidationError) Error() string { return e.Err.Error() }

func (e *ValidationError) Unwrap() error { return e.Err }

func newValidationError(format string, args ...any) error {
	return &ValidationError{Err: fmt.Errorf(format, args...)}
}
