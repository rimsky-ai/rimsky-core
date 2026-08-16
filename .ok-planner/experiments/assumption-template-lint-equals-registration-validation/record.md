---
experiment: assumption-template-lint-equals-registration-validation
commit: PENDING
---

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
