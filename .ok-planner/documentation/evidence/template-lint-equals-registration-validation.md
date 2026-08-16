---
trap: template-lint-equals-registration-validation
release: d977250c
---
# Evidence set — `rimsky template lint` (and `POST /v1/templates/validate`) catches everything registration would reject, offline and without a live stack.

Source of the prior: name-promise — `rimsky template lint` beside `rimsky template register` and a `validate` route

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-template-lint-equals-registration-validation)

# Lint against registration validation

## What it ran against

The CLI built from this tree and one `rimsky-all-in-one` container from this
tree's image set. The first question — can lint run offline — is asked by
linting a valid template with the endpoint pointed at a closed port. The
second — does lint report what registration rejects — puts nine defective
templates through `template lint` and `template register` against the same
live deployment and compares the finding paths and exit codes: an undeclared
executor, an undeclared claim producer, a duplicate node type, a dangling
subscribe, an uncompilable `params_schema`, an undeclared sent message, an
out-of-grammar duration, an unknown top-level key, and a dangling graph
reference.

## What was observed

Lint cannot run offline. `rimsky template lint <file>` with no deployment
returns `Post "http://…/v1/templates/validate": connection refused` — the verb
uploads the template to the control API and has no local path. That is not
incidental: the findings it returns are deployment-specific, naming "the
operator's `executors:` block" and "the operator's `claim_producers:` block",
so the same file lints clean against one deployment and fails against another.

Against a live deployment the two verbs agree completely. All nine cases
matched on both the finding paths and the accept/reject verdict — seven
produced identical error paths (and, for the claim-producer case, an identical
warning alongside), and two were refused at YAML parse by both before
validation began. Nothing registration rejected passed lint, in either
direction. 10 checks, 9 pass, 1 fail.

Runnables: `src:.ok-planner/experiments/assumption-template-lint-equals-registration-validation/` at the stamped commit.
