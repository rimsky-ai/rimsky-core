---
trap: npm-package-ships-generated-clients
release: d977250c
---
# Evidence set — `@rimsky-ai/protocols` ships generated TypeScript stubs, not just `.proto` files, so a TS executor can be written without running protoc.

Source of the prior: ecosystem-prior — an npm package named for protocols in a gRPC ecosystem

## What the audit ran and observed (assumption record)

Experiment `assumption-npm-package-ships-generated-clients` (seven checks, none
failing) packed the artifact this tree would publish and downloaded the one a
consumer installs today (`npm pack @rimsky-ai/protocols@latest`), listed both
tarballs, and imported the package's entry point from node. The prior does not
hold. The package is ten `.proto` files, a `package.json`, and two code files;
the published tarball has the same thirteen entries. Nothing in either matches
any generated-stub naming. `index.d.ts` declares exactly two exports and the
import hands back exactly `protoDir` and `protoPath` — a directory to point a
proto loader at, and a function that joins a filename onto it. A TypeScript
service author gets the wire definitions and a path, and still has to run a
protobuf toolchain over them (the package's own description says as much: it is
for `@grpc/proto-loader` and other protobuf toolchain consumers) — which is
workable, but it is not the "install and import the client" the package name
suggests.

## Experiment record (experiment:assumption-npm-package-ships-generated-clients)

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

Runnables: `src:.ok-planner/experiments/assumption-npm-package-ships-generated-clients/` at the stamped commit.
