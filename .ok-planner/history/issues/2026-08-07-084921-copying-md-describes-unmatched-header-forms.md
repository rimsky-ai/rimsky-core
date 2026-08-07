---
issue: copying-md-describes-unmatched-header-forms
kind: human
category: licensing
artifacts:
  - decision:licensing-enforced-by-license-lint
status: repaired
opened: 2026-08-07T08:49:21Z
github: https://github.com/rimsky-ai/rimsky-core/issues/73
---

# Does COPYING.md still describe header forms that match no stamper template?

Yes, confirmed on the current tree: `COPYING.md`'s "How to tell which
license a file is under" section described Apache files as carrying an
`"Apache License, Version 2.0"` prose header and AGPL files a
`"Dual-licensed under ..."` prose header — neither string matched any
`tools/license-check/headers.go` stamper template (`.go`/`.ts`/`.sql`/`.sh`
templates are all `SPDX-License-Identifier:` form; only `.proto`'s Apache
template still used the retired prose form, itself fixed in the sibling
issue `proto-files-carry-no-spdx-identifier` in this same pass).

`tools/license-check/headers.go`'s constants are the mechanical source of
truth for what a compliant header looks like; the rules leave exactly one
compliant description once the proto template is also brought to SPDX form
(same pass): every stamped file carries an `SPDX-License-Identifier:` line.
No commitment changed — `decision:licensing-enforced-by-license-lint`
governs the import-boundary lint, not header wording, so this was a pure
prose-to-code alignment.

**Change:** `COPYING.md`'s header-form bullet now reads "Apache files carry
a `SPDX-License-Identifier: Apache-2.0` header; AGPL files carry a
`SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial`
header."

**Verified:** `make license-lint` (0 violations, 150 apache / 1610 agpl
files) and manual comparison of the new bullet against every
`apacheHeader*`/`agplHeader*` constant in `tools/license-check/headers.go`.
