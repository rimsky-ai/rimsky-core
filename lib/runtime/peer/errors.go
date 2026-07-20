// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type ProducerCallError struct {
	ProducerName string
	Method       string
	ErrorClass   string
	Message      string
	Underlying   error
}

func (e *ProducerCallError) Error() string {
	return fmt.Sprintf("producer %q: %s: %v", e.ProducerName, e.Method, e.Underlying)
}

func (e *ProducerCallError) Unwrap() error { return e.Underlying }

func NewProducerCallError(producerName, method string, err error) *ProducerCallError {
	return &ProducerCallError{
		ProducerName: producerName,
		Method:       method,
		ErrorClass:   extractErrorClass(err),
		Message:      extractStatusMessage(err),
		Underlying:   err,
	}
}

func extractErrorClass(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info.Reason
		}
	}
	return ""
}

func extractStatusMessage(err error) string {
	if err == nil {
		return ""
	}
	st, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}
	return st.Message()
}

func GateSplitScope(caps claimproducer.Capabilities) error {
	if caps.SupportsSplitScope {
		return nil
	}
	return claimproducer.ErrSplitScopeUnsupported
}

func GateScopesConflict(caps claimproducer.Capabilities, a, b []byte) (fallback, gated bool) {
	if caps.SupportsScopesConflict {
		return false, false
	}
	return claimproducer.ErrScopesConflictUnsupportedFallback(a, b), true
}
