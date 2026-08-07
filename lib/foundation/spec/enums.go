// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type BackoffKind string

const (
	BackoffLinear      BackoffKind = "linear"
	BackoffExponential BackoffKind = "exponential"
)

type JitterKind string

const (
	JitterNone      JitterKind = "none"
	JitterPlusMinus JitterKind = "plus_minus"
)

// @concept: cascade
// @decision: mode-default-most-recent
type CascadeMode string

const (
	CascadeModeMostRecent        CascadeMode = "most-recent"
	CascadeModeSequenced         CascadeMode = "sequenced"
	CascadeModeIdempotentQueue   CascadeMode = "idempotent-queue"
	CascadeModeIdempotentSettled CascadeMode = "idempotent-settled"
)

const (
	ErrorClassTemplateResolutionFailed  = "template_resolution_failed"
	ErrorClassTemplateValidationFailed  = "template_validation_failed"
	ErrorClassExecutorSchemaUnavailable = "executor_schema_unavailable"
	ErrorClassAttributesSchemaFailed    = "attributes_schema_failed"
	ErrorClassUnresolvedExecutor        = "unresolved_executor"
	ErrorClassExecutorSyncTimeout       = "executor_sync_timeout"
	ErrorClassExecutorProtocolViolation = "executor_protocol_violation"
	// @decision: terminal-error-abandoned-as-error-class
	ErrorClassAbandoned     = "abandoned"
	ErrorClassAcquirePrefix = "acquire/"
)

// @concept: error-policy
var RuntimeSynthesizedErrorClasses = []string{
	ErrorClassTemplateResolutionFailed,
	ErrorClassTemplateValidationFailed,
	ErrorClassExecutorSchemaUnavailable,
	ErrorClassAttributesSchemaFailed,
	ErrorClassUnresolvedExecutor,
	ErrorClassExecutorSyncTimeout,
	ErrorClassExecutorProtocolViolation,
	ErrorClassAbandoned,
}
