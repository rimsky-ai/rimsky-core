# Copying and Licensing

Rimsky is Copyright © 2026 Fall Guy Consulting.

This document explains, in plain terms, how Rimsky is licensed and how to
comply. It is a guide, not the license itself — the binding texts are
`LICENSE.apache`, `LICENSE.agpl`, and `COPYRIGHT`. Where this guide and those
files differ, those files control.

## The short version

| You are…                                                              | Your obligation                                                                 |
| --------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| Implementing or linking against the `lib/protocols/` module               | Apache 2.0 — permissive, no copyleft.                                           |
| Running, modifying, or distributing the rest of Rimsky under open terms | AGPL-3.0-or-later — copyleft, including network-service source disclosure (§13). |
| Doing the above but unable or unwilling to accept the AGPL            | Buy a Fall Guy Consulting commercial license.                                   |

## One bright line

Rimsky ships under two licenses, divided by a single boundary:

- **`lib/protocols/` is Apache License 2.0.** This module is the wire contract —
  the protocol IDL, the generated bindings, and the helper/conformance
  packages a consumer implements or links against to speak to Rimsky. It is
  deliberately permissive so that anyone can build a service that talks to
  Rimsky, or embed the protocol surface, without taking on copyleft
  obligations. `examples/` — copy-and-modify protocol examples — carries the
  same Apache terms, as a deliberate carve-out kept permissive so consumers
  can build on them.

- **Everything else Rimsky ships is AGPL-3.0-or-later, or a Fall Guy
  Consulting commercial license.** The orchestrator binaries *and* the
  reference services are real, runnable artifacts — not just illustrations.
  They are meant to be used as-is, and that use carries the AGPL's copyleft.

The boundary is enforced mechanically, not just described here. `licensing.yml`
is the source-of-truth path map; `tools/license-check` verifies that every
file carries the right per-file header, that no Apache file imports an AGPL
package, that every path the map names still exists, and that every
third-party dependency in an Apache module's build closure carries a
permitted permissive license. Because the `lib/protocols/` module imports
nothing internal, the Apache code forms a single closed island — there is no
code path by which Apache-licensed source depends on AGPL-licensed source.

## Why the line is drawn here

The only thing a consumer is ever *required* to implement or link against is in
`lib/protocols/`. That is the integration surface, so it is permissive.

Everything else — including the reference services — is something Rimsky offers
you ready to run. Using a real service Rimsky ships is using Rimsky, and that is
the AGPL boundary. If you want to integrate with Rimsky without accepting the
AGPL and without a commercial license, the escape hatch is built in: implement
the protocols from your own process. The permissive `lib/protocols/` module exists
precisely so that path is open to everyone.

## What each license requires

**Apache 2.0** (`lib/protocols/`, plus the `examples/` carve-out).
Permissive. You may use, modify, and redistribute under the Apache terms,
including in closed-source products. Preserve the license and copyright
notices and the `NOTICE` file; see `LICENSE.apache`.

**AGPL-3.0-or-later** (everything else, by default). Strong copyleft. If you
modify Rimsky and convey it, or **make it available to users over a network**,
you must offer those users the corresponding source under the AGPL — this is
the §13 network-use clause that distinguishes the AGPL from the GPL. See
`LICENSE.agpl`. No action is required to use Rimsky under these terms; the AGPL
is the default.

**Fall Guy Consulting commercial license** (alternative to the AGPL, by
agreement). For organizations that want to use, modify, or distribute the
orchestrator and services without the AGPL's §5 (copyleft) or §13
(network-service source disclosure) obligations. This is a separately
negotiated grant over the same AGPL-licensed code; it does not change the
Apache terms on `lib/protocols/`. Contact **licensing@fallguyconsulting.com**.

## How to tell which license a file is under

1. **The per-file header.** Apache files carry a `SPDX-License-Identifier:
   Apache-2.0` header; AGPL files carry a `SPDX-License-Identifier:
   AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial` header.
2. **`licensing.yml`.** The authoritative path map. Classification is
   longest-prefix-match, so a specific subdirectory can override its parent.

If the header and the map ever disagree, that is a bug — `make license-lint`
exists to catch it.

## Contributions

Contributions are accepted under the Rimsky Contributor Certificate in
`CONTRIBUTING.md` — a single per-commit `Rimsky-Cert` sign-off that grants Fall
Guy Consulting the rights necessary to maintain this multi-license structure. By
contributing you agree your contribution may be distributed under both the
Apache and AGPL/commercial terms as appropriate to where it lands.

## Trademarks

"Rimsky" and the Rimsky logo are trademarks of Fall Guy Consulting. A software
license is not a trademark license. See `TRADEMARKS.md` for the usage policy.

## Canonical files

| File             | What it is                                                        |
| ---------------- | ----------------------------------------------------------------- |
| `LICENSE.apache` | Full Apache License 2.0 text.                                     |
| `LICENSE.agpl`   | Full GNU AGPL v3 text.                                            |
| `COPYRIGHT`      | The formal per-layer copyright notice (referenced by file headers). |
| `NOTICE`         | Apache §4(d) attribution notice.                                  |
| `licensing.yml`  | Machine-readable boundary map enforced by `tools/license-check`. |
| `CONTRIBUTING.md` | Contributor guide + the Rimsky Contributor Certificate.          |
| `TRADEMARKS.md`  | Trademark usage policy.                                           |
