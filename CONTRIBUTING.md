# Contributing to Rimsky

Thanks for your interest in contributing to Rimsky. This guide covers the
two procedural requirements (CLA signature, DCO sign-off) and links the
deeper technical conventions of the codebase.

## 1. Sign the CLA

Rimsky is multi-licensed (Apache 2.0 + AGPL-3.0-or-later + a Fall Guy
Consulting commercial license — see `COPYRIGHT` for the per-layer
breakdown). To preserve the multi-license structure, every contributor
must sign the [Contributor License Agreement](CLA.md) once.

Signing is automated. The [cla-assistant.io](https://cla-assistant.io/)
GitHub bot comments on your first pull request with a one-click signing
link. Pull requests are blocked from merging until the bot records your
signature. One signature per contributor (not per pull request); a future
material change to the CLA text will prompt a fresh signature.

Read the CLA in full before signing. The two items that distinguish it
from the upstream Apache ICLA are §4 (Relicensing Grant — lets Fall Guy
Consulting offer the contributed code under the commercial license track)
and §9 (Versioning).

## 2. Sign each commit (DCO)

Every commit must carry a `Signed-off-by:` trailer per the
[Developer Certificate of Origin](https://developercertificate.org/).
Add it automatically with `git commit -s`; configure your editor to do
this by default if you contribute often. The DCO sign-off is enforced by
a separate bot (`probot/dco`) and is independent of the CLA — both must
pass before merge.

If you forgot to sign off on a commit, amend it (`git commit --amend
--signoff`) or rebase the offending range. The DCO bot's check output
includes the exact incantation.

## 3. How to contribute

- **Open an issue first** for non-trivial changes. A short note describing
  the problem and your proposed approach saves rework if the design or
  scope needs adjustment.
- **Follow the cold-read conventions** documented in
  `cold-read/cold-read-style-guide.md` and the cheatsheet at
  `.claude/rules/cold-read-cheatsheet.md`. The short version: organize by
  feature not layer, keep files under ~500 lines and functions under
  ~100, max 3 levels of nesting, prefer tracked duplication
  (`@source:` annotations) over hidden coupling.
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

## 4. Reporting security issues

Please do not file public issues for security vulnerabilities. Email
`security@fallguyconsulting.com` with a description of the issue and steps
to reproduce. We respond within five business days; coordinated
disclosure is the norm.

## 5. Questions

For licensing or trademark questions, contact
`licensing@fallguyconsulting.com`. For technical questions, open an issue
or start a discussion on the project's GitHub.
