// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// errors.go — typed error for remote claim-producer call failures.
// Translates a gRPC status into a Go error that carries the rimsky
// error_class so the supervisor's error-policy chain (applyErrorPolicy)
// can route claim-producer faults through the template's `error_types:`
// chain, exactly as executor StreamClose{Error, error_class} faults are
// routed. The error_class travels on the wire as a google.rpc.ErrorInfo
// detail whose Reason field is the class name; a host-agent-proxy
// fronting the claim-producer protocol stamps the proxy-mediated error
// classes (spawn_failed, host_agent_disconnected, etc.) there.

package peer

import (
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// ProducerCallError is returned by remote producer-protocol calls
// (claim-producer verbs, data_processing mix-in verbs) when the
// underlying gRPC call failed. It carries the rimsky error_class
// extracted from the gRPC status's ErrorInfo detail (or "" if none)
// and the producer's own status message. Two consumers read it:
//   - the supervisor's error-policy chain (applyErrorPolicy) reads
//     ErrorClass to consult the template's error_types: chain;
//   - the control-api's writeError reads ProducerName / ErrorClass /
//     Message so an API-triggered producer failure crosses the HTTP
//     boundary intact instead of collapsing into a bare 500.
type ProducerCallError struct {
	ProducerName string
	Method       string
	ErrorClass   string
	Message      string
	Underlying   error
}

func (e *ProducerCallError) Error() string {
	return fmt.Sprintf("remote producer %q: %s: %v", e.ProducerName, e.Method, e.Underlying)
}

func (e *ProducerCallError) Unwrap() error { return e.Underlying }

// NewProducerCallError builds a ProducerCallError from a failed remote
// producer call, extracting the rimsky error_class (ErrorInfo.Reason)
// and the producer's own status message from the gRPC status. The
// single constructor keeps every translation site — claim-producer
// verbs, data_processing verbs — carrying the same structured fields,
// so no boundary discards what the producer transmitted.
func NewProducerCallError(producerName, method string, err error) *ProducerCallError {
	return &ProducerCallError{
		ProducerName: producerName,
		Method:       method,
		ErrorClass:   extractErrorClass(err),
		Message:      extractStatusMessage(err),
		Underlying:   err,
	}
}

// extractErrorClass walks the gRPC status details for a
// google.rpc.ErrorInfo entry and returns its Reason as the
// rimsky error_class. Returns "" when no ErrorInfo is attached.
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

// extractStatusMessage returns the gRPC status message (the
// producer-authored description) for err, or err.Error() when err is
// not a gRPC status error. status.FromError never returns a nil
// status for a non-nil error, so the message is always populated.
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
