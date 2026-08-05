---
issue: copyright-describes-pre-spdx-header-form
kind: human
category: licensing
artifacts:
  - decision:licensing-dual-apache-agpl
  - decision:licensing-enforced-by-license-lint
status: repaired
opened: 2026-08-04T20:55:08Z
github: https://github.com/rimsky-ai/rimsky-core/issues/40
---

# `COPYRIGHT` still describes the prose per-file license header the tree replaced with SPDX

## Question

`COPYRIGHT` described each layer's per-file marker as a prose header
("Apache License, Version 2.0" / "Dual-licensed under AGPL-3.0-or-later or
a Fall Guy Consulting commercial license"), but every source file actually
carries an SPDX identifier line instead. Which form is authoritative, and
should `COPYRIGHT` be corrected to match?

## Repair

The rules fully determine this: `tools/license-check/headers.go` — the
license-lint enforcement binary itself — defines the exact per-file marker
text as `SPDX-License-Identifier: Apache-2.0` and
`SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial`
(the `apacheHeaderGo`/`agplHeaderGo` constants stamped by `make
license-stamp`), and every sampled source file (`lib/protocols/enroll/trust.go`,
`lib/foundation/persistence/node_runs.go`, `cmd/rimsky/conformance.go`)
carries exactly that form. `decision:licensing-dual-apache-agpl` and
`decision:licensing-enforced-by-license-lint` govern the license split and
its enforcement mechanism, not the header's literal text, so this is a
stale-description repair only — no commitment changes.

Corrected `COPYRIGHT` (root file, not under `design/`) to name the SPDX
form verbatim in both places it previously quoted the retired prose
header, and added a sentence noting `LicenseRef-FallGuy-Commercial` is a
non-registry SPDX pointer to the commercial alternative, not a public
license text (closing the issue's own candidate resolution).

Verified: `tools/license-check` build/tests untouched by this change
(`COPYRIGHT` is not a lint input); `gofmt`/`go vet` not applicable (no Go
changed). Diff reviewed against `tools/license-check/headers.go`'s marker
constants for exact-string match.
