---
experiment: assumption-npm-package-ships-generated-clients
commit: PENDING
---

# What `@rimsky-ai/protocols` puts in a consumer's node_modules

## What it ran against

The npm artifact itself, twice: `npm pack` over this tree's `lib/protocols`,
which produces exactly the tarball a publish would upload, and `npm pack
@rimsky-ai/protocols@latest`, which downloads the tarball a consumer installs
today. Both tarballs are listed entry by entry. The package's entry point is
then imported by node to see what a consumer actually gets back.

## What was observed

Seven checks, none failing.

The tarball is ten `.proto` files, a `package.json`, and two code files —
`index.js` and `index.d.ts`. Nothing in it looks like a generated stub under any
of the usual names. The published tarball has the same shape: thirteen entries,
ten of them `.proto`, and the same two code files.

The two code files are path helpers. `index.d.ts` declares exactly two exports,
and importing the package from node hands back exactly `protoDir` and
`protoPath` — a directory to point a proto loader at, and a function that joins
a filename onto it. There is no message type, no client, and no service
definition in the package.
