// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// errors.go — classed errors the filesystem store transmits across the
// ClaimProducer wire. The server boundary translates a *ClassedError
// into a gRPC status carrying a google.rpc.ErrorInfo detail
// (Reason = Class), the exact shape rimsky's claim-producer client
// decodes — so rimsky's error-policy chain AND the control-api's
// producer-error responses both see the store's own class instead of
// an anonymous failure.

package store

import "fmt"

// RootUnavailableClass is the error class the store names when its
// configured backing root is missing or not writable at verb time —
// the operator-misconfiguration case (root path wrong, volume not
// mounted, mount gone read-only). Naming the class here is what lets
// an operator diagnose the problem from the API response or their
// `error_types:` routing instead of grepping rimsky's logs.
const RootUnavailableClass = "fs/root_unavailable"

// ClassedError carries a rimsky error_class alongside a store-side
// failure so the gRPC server boundary can translate it into a
// google.rpc.ErrorInfo detail (Reason = Class) WITHOUT string-matching
// the message. The Error() string still names the class so any human
// reading a log sees it inline.
//
// @source: lib/services/stores/postgres/store/staging.go:ClassedError
// @diverged: true
// @reason: adds Unwrap so errors.As/Is can reach the wrapped cause;
// the postgres copy predates that need.
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
