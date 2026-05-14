// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// QualityRuleSpec is a single quality rule declaration: a rule type
// (builtin name or "custom"), a free-form config payload consumed by
// the evaluator, and a severity classifying whether a failure should
// block a commit or merely warn.
//
// QualityRuleSpec values live inside TemplateNodeDef.QualityRules and
// are serialized as part of the persisted rimsky_templates.spec JSON.
// The evaluator interface that consumes these specs lives in
// graph/qualityrule (the algorithm layer); this package defines only
// the data shape.
type QualityRuleSpec struct {
	Type     string         `yaml:"type" json:"type"`
	Config   map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
	Severity Severity       `yaml:"severity,omitempty" json:"severity,omitempty"` // default "error"
}

// QualityRuleFailure is the outcome of a single QualityRuleSpec that
// did not pass, carrying enough context for the caller to log or
// surface the problem.
type QualityRuleFailure struct {
	RuleType string
	Config   map[string]any
	Severity Severity
	Details  string
}

// QualityRuleEvalInput is the payload handed to an evaluator.
// NewData is the proposed data; PreviousData is nil when there is no
// prior version. Cfg is the QualityRuleSpec.Config for the rule being
// evaluated.
type QualityRuleEvalInput struct {
	NewData      any
	PreviousData any
	Cfg          map[string]any
}
