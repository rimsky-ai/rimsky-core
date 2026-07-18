---
tension: quality-rule-severity-string-footgun
category: unspecified
status: resolved
affects:
  - validation
  - attribute
---

# Quality-rule severity is partitioned by exact-string equality on `"warning"` — any other value silently blocks

## Resolution

Resolved by user ruling (2026-07-17, design-call walkthrough). The subsystem this tension targeted no longer exists: the core quality-rule feature was dissolved, and its capability lives in a bundled utility executor (the shape-checks verifier) that owns its own config parsing. That successor implements what the resolution candidates asked for: severity is a typed value parsed against the closed error/warning set (empty defaults to error), and an unrecognized value is rejected with an error naming the valid values — a loud failure at the node's first dispatch instead of a silent promotion to blocking. Severity is executor userdata (free-form authored YAML, opaque to rimsky), so parse-time rejection inside the executor is the correct enforcement point; registration-time rejection is available to the executor via the validation role if it ever advertises it, as an option rather than an obligation.

## What is muddy

`graph/qualityrule/eval/rules.go::EvaluateAll` partitions `Failure` slices into `warnings` and `errors` by comparing `Spec.Severity` to the literal string `"warning"`. The default in `graph/qualityrule/spec.go` is the string `error`, but nothing in the type system or validator constrains the field to that pair. An operator who writes `severity: warn` (typo), `severity: Warning` (case), `severity: info` (different policy), or leaves it empty in a YAML where `error` was intended all get the same behavior: the failure goes into the blocking `errors` slice and the commit fails.

The severity vocabulary lives only in this single comparison plus the default constant. There is no `shared.Severity` type, no enum validator at template registration, no documented allowed-values list.

## Why it matters

Two cold-read problems: (1) a reader inspecting `Spec.Severity string` cannot tell which values are meaningful without grep'ing `EvaluateAll`; (2) operators get silent surprises in production when a typo elevates a should-be-advisory rule into a blocking one. Quality rules are commit-time gates — promoting "warning" to "error" by accident blocks legitimate writebacks. A typed enum or template-registration-time validator would surface the misconfiguration where it's authored, not at the first failing run.

## Resolution candidates (do NOT pick)

- Model quality-rule severity as a closed enumerated type (the warning/error pair) and require the configured severity to be one of its members at template registration, so an unrecognized value is rejected where it is authored.
- Validate the severity value against a fixed allowed-set at template registration, surfacing typos at authoring time rather than at the first failing commit.
- Document the allowed severity values as part of the quality-rule (verifier-executor) pattern, so authors know which strings are meaningful and that anything else defaults to blocking.

## Evidence

- `_discover/quality-rules-and-attribute-validation.md` Observations bullet "severity defaults to `error`".
- `graph/qualityrule/eval/rules.go` — the partition predicate.
- `graph/qualityrule/spec.go` — the default `error`.

