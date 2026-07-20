// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const (
	ErrorClassTemplateResolutionFailed  = "template_resolution_failed"
	ErrorClassTemplateValidationFailed  = "template_validation_failed"
	ErrorClassExecutorSchemaUnavailable = "executor_schema_unavailable"
	ErrorClassAttributesSchemaFailed    = "attributes_schema_failed"
	ErrorClassUnresolvedExecutor        = "unresolved_executor"
	ErrorClassExecutorSyncTimeout       = "executor_sync_timeout"
	ErrorClassAcquirePrefix             = "acquire/"
)

var RuntimeSynthesizedErrorClasses = []string{
	ErrorClassTemplateResolutionFailed,
	ErrorClassTemplateValidationFailed,
	ErrorClassExecutorSchemaUnavailable,
	ErrorClassAttributesSchemaFailed,
	ErrorClassUnresolvedExecutor,
	ErrorClassExecutorSyncTimeout,
}
