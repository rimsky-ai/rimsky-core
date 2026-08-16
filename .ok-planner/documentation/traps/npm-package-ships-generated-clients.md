---
trap: npm-package-ships-generated-clients
release: d977250c
demonstration: experiment:assumption-npm-package-ships-generated-clients
---
## Assumption

As TypeScript service author, I would take it that `@rimsky-ai/protocols` ships generated TypeScript stubs, not just `.proto` files, so a TS executor can be written without running protoc.

ecosystem-prior — an npm package named for protocols in a gRPC ecosystem

## Actual behavior

Experiment `assumption-npm-package-ships-generated-clients` (seven checks, none
failing) packed the artifact this tree would publish and downloaded the one a
consumer installs today (`npm pack @rimsky-ai/protocols@latest`), listed both
tarballs, and imported the package's entry point from node. The prior does not
hold. The package is ten `.proto` files, a the experiment, and two code files;
the published tarball has the same thirteen entries. Nothing in either matches
any generated-stub naming. `index.d.ts` declares exactly two exports and the
import hands back exactly `protoDir` and `protoPath` — a directory to point a
proto loader at, and a function that joins a filename onto it. A TypeScript
service author gets the wire definitions and a path, and still has to run a
protobuf toolchain over them (the package's own description says as much: it is
for `@grpc/proto-loader` and other protobuf toolchain consumers) — which is
workable, but it is not the "install and import the client" the package name
suggests.
