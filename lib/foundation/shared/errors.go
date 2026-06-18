// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"errors"
	"fmt"
)

var (
	ErrTemplateValidation  = errors.New("template validation failed")
	ErrTemplateNotFound    = errors.New("template not found")
	ErrInstanceNotFound    = errors.New("instance not found")
	ErrNodeNotFound        = errors.New("node not found")
	ErrInstanceKeyConflict = errors.New("instance_key already exists for template")
	ErrTemplateInUse       = errors.New("template has live instances")
	ErrNodeRunning         = errors.New("node is currently running")
	ErrNodeApplication     = errors.New("node application error")
	ErrRollbackUnsupported = errors.New("rollback unsupported by resource implementation")
	ErrExecutorNotFound    = errors.New("executor not found in supervisor config")

	// @concept: breakpoint
	ErrBreakpointNotFound    = errors.New("breakpoint not found")
	ErrBreakpointHitNotFound = errors.New("breakpoint hit not found")
	ErrResumeOverlayInvalid  = errors.New("resume overlay invalid")
	ErrInstanceNotPaused     = errors.New("instance not paused")
	ErrInstanceAlreadyPaused = errors.New("instance already paused")
)

type RimskyError struct {
	Err     error
	Message string
	Fields  map[string]any
}

func (e *RimskyError) Error() string { return fmt.Sprintf("%s: %v", e.Message, e.Err) }
func (e *RimskyError) Unwrap() error { return e.Err }

func Wrap(err error, message string, fields map[string]any) *RimskyError {
	return &RimskyError{Err: err, Message: message, Fields: fields}
}
