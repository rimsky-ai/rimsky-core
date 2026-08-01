// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"errors"
	"fmt"
)

type ClassedError interface {
	error
	ErrorClass() string
}

var ErrSplitScopeUnsupported = errors.New("split_scope unsupported by this producer")

var ErrScopesConflictUnsupported = errors.New("scopes_conflict unsupported by this producer")

func (c Capabilities) EnforceOpenWriteSemantics(producerKind, producerName string, out OpenOutcome) (OpenOutcome, error) {
	if !out.Available {
		return out, nil
	}
	rws := out.Result.RealizedWriteSemantics
	if rws == WriteSemanticsUnknown {
		return OpenOutcome{}, fmt.Errorf("%s %q: Open: realized_write_semantics is UNKNOWN (producer must declare a concrete value)", producerKind, producerName)
	}
	if !c.Contains(rws) {
		return OpenOutcome{}, fmt.Errorf("%s %q: Open: realized_write_semantics %q not in advertised envelope %v", producerKind, producerName, rws, c.WriteSemanticsAllowed)
	}
	return out, nil
}

func ErrScopesConflictUnsupportedFallback(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
