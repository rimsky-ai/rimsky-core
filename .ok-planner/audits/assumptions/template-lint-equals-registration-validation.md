---
assumption: template-lint-equals-registration-validation
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `rimsky template lint` (and `POST /v1/templates/validate`) catches everything registration would reject, offline and without a live stack.

As template author, I would take it that `rimsky template lint` (and `POST /v1/templates/validate`) catches everything registration would reject, offline and without a live stack.

## Source

name-promise — `rimsky template lint` beside `rimsky template register` and a `validate` route

## What a run would observe

lint a template referencing an undeclared executor and an out-of-vocabulary error class, then register it, and compare the findings.

## Measured

`.ok-planner/experiments/assumption-template-lint-equals-registration-validation`
— built for this run — put nine defective templates through `template lint`
and `template register` against one live `rimsky-all-in-one` from this tree's
image set, and separately asked lint to run with no deployment at all.

The second half of the prior holds and the first does not. On all nine cases —
undeclared executor, undeclared claim producer, duplicate node type, dangling
subscribe, uncompilable `params_schema`, undeclared sent message,
out-of-grammar duration, unknown top-level key, dangling graph reference —
lint and register reported the same finding paths and the same verdict; two
were refused at YAML parse by both. Nothing registration rejects slips past
lint.

But lint is not an offline check. `rimsky template lint <file>` with no
deployment reachable returns `Post ".../v1/templates/validate": connection
refused`: the verb uploads the template and the control API does the work.
That is structural, not incidental — the findings name "the operator's
`executors:` block" and "the operator's `claim_producers:` block", so the
answer depends on which deployment is asked, and the same file lints clean
against one and fails against another. A template author in CI with no
deployment cannot lint at all. 10 checks, 9 pass, 1 fail.
