# Contributing to Rimsky

Thanks for your interest in contributing to Rimsky. Contributing takes
one step beyond a normal pull request: **sign off each commit** with the
Rimsky Contributor Certificate. That single sign-off replaces the
separate CLA signature and DCO sign-off that earlier versions required —
no separate agreement to sign, no signing bot, no click-through.

## 1. Sign off your commits (Rimsky Contributor Certificate)

Every commit in a pull request must carry a `Rimsky-Cert` trailer:

```
Rimsky-Cert: Your Name <you@example.com>
```

By adding it you certify the Rimsky Contributor Certificate (below):
that you have the right to submit the contribution, and that you grant
Fall Guy Consulting the licenses it needs to ship Rimsky under its
multi-license structure (including the commercial track). It folds the
old DCO provenance certification and the old CLA rights grant into one
line.

### Adding the trailer

- **New commit:** `git commit --trailer "Rimsky-Cert: Your Name <you@example.com>"`
  (or just type the line at the end of the commit message).
- **A whole existing branch** (backfill, then force-push):
  ```
  git rebase --exec 'git commit --amend --no-edit \
    --trailer "Rimsky-Cert: Your Name <you@example.com>"' origin/main
  ```
- To make it automatic, add a `prepare-commit-msg` hook in your clone
  that appends the trailer.

### Enforcement

A GitHub Action (`.github/workflows/contributor-cert.yml`) checks every
non-merge commit in a pull request for a well-formed trailer and reports
the **Rimsky-Cert sign-off** check. That check is required on the
protected branch, so a pull request whose commits are not all signed off
cannot be merged.

### The certificate

```
Rimsky Contributor Certificate — Version 1.0

By adding a `Rimsky-Cert` sign-off to a commit you submit to this
project, you certify the following.

Provenance (the standard DCO certification):

  a. The contribution was created in whole or in part by you, and you
     have the right to submit it under the licenses in this repository; or
  b. It is based on previous work covered, to the best of your knowledge,
     under an appropriate open source license, and you have the right
     under that license to submit it, with or without modifications,
     under the licenses in this repository; or
  c. It was provided to you by someone who certified (a), (b), or (c),
     and you have not modified it.
  d. You understand this project and your contribution are public, and a
     record of the contribution — including your sign-off and the
     personal information in it — is kept indefinitely and may be
     redistributed consistent with this project's licenses.

Grant. In consideration of the project reviewing, accepting, and
distributing your contribution (and of US$1.00 payable on demand if you
ever request it), you also agree that:

  e. You grant Fall Guy Consulting and all recipients of the project a
     perpetual, worldwide, non-exclusive, royalty-free, irrevocable
     copyright and patent license to your contribution, under the
     project's licenses (see COPYRIGHT).
  f. You grant Fall Guy Consulting a perpetual, worldwide, non-exclusive,
     royalty-free, irrevocable, sublicensable right to relicense your
     contribution under any terms, including Apache-2.0,
     AGPL-3.0-or-later, and a Fall Guy Consulting commercial license.

You keep ownership of your contribution. This is a license, not an
assignment.
```

## 2. How to contribute

- **Open an issue first** for non-trivial changes. A short note describing
  the problem and your proposed approach saves rework if the design or
  scope needs adjustment.
- **Follow the Plumbline coding conventions** materialized at
  `.claude/rules/plumbline-cheatsheet.md` (installed and refreshed by
  the [Plumbline plugin](https://github.com/fallguyconsulting/plumbline)
  via `/plumbline:affirm`). The short version: organize by feature not
  layer, keep files under ~500 lines and functions under ~100, max 3
  levels of nesting, strict DRY through named searchable abstractions
  (no logic copied between sites; `@source:` annotations only mark
  intentionally divergent mirrors), every constraint backed by a lint
  rule, test, or type, and comments only where they carry a structured
  tag (`@constraint:`, `@deliberate:`, `@agent-contract`, ...).
- **Run `make license-lint` locally** before submitting. The license-check
  binary verifies that no Apache-classified package imports an
  AGPL-classified package and that every source file carries the correct
  license header. CI runs the same check.
- **Run `make lint` and `go test ./...`** locally. The tests use
  testcontainers-go to spin up real Postgres for storage and scenario
  tests, so a working Docker socket is required.
- **Match the project's commit-message style.** Imperative mood, short
  subject line (under ~70 chars), body explains the why if it isn't
  obvious from the diff.
- **Per-layer conventions matter.** If your change adds or moves code
  across the licensing boundary (e.g. moves a type from an AGPL package
  to an Apache package), update `licensing.yml` in the same PR and
  re-stamp headers (`make license-stamp`).

## 3. Reporting security issues

Please do not file public issues for security vulnerabilities. Email
`security@fallguyconsulting.com` with a description of the issue and steps
to reproduce. We respond within five business days; coordinated
disclosure is the norm.

## 4. Questions

For licensing or trademark questions, contact
`licensing@fallguyconsulting.com`. For technical questions, open an issue
or start a discussion on the project's GitHub.
