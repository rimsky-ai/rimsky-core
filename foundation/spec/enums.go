// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package spec

// Severity classifies the consequence of a failure: an "error" failure
// blocks the commit, a "warning" failure is logged but allows the run
// to succeed. Used by policy-action declarations and by service-side
// observability events.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// BackoffKind names the curve used by ComputeDelay when scheduling a
// retry. Linear grows base*N; exponential grows base*2^(N-1).
type BackoffKind string

const (
	BackoffLinear      BackoffKind = "linear"
	BackoffExponential BackoffKind = "exponential"
)

// JitterKind names how computed retry delays are jittered.
// PlusMinus multiplies the base delay by a uniform random in [0.5, 1.5).
type JitterKind string

const (
	JitterNone      JitterKind = "none"
	JitterPlusMinus JitterKind = "plus_minus"
)
