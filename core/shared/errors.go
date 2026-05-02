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
	ErrIllegalTransition   = errors.New("illegal state transition") // blessed-invariant (§17)
	ErrRollbackUnsupported = errors.New("rollback unsupported by resource implementation")
	ErrExecutorNotFound    = errors.New("executor not found in supervisor config")
)

// RimskyError wraps a sentinel with context fields for structured logging.
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
