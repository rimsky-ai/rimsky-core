// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// @rimsky-ai/protocols — path helper for the Rimsky v1 wire-protocol .proto
// files. The package ships the raw .proto definitions; consumers load them at
// runtime with @grpc/proto-loader (or any protobuf toolchain). This module
// resolves their on-disk location inside the installed package so callers
// never hardcode a node_modules path.
//
// CommonJS consumers that don't want the ESM helper can resolve a proto
// directly via the "./proto/*" export, e.g.:
//   require.resolve("@rimsky-ai/protocols/proto/v1/executor.proto")

import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));

// Absolute path to the directory holding the v1 .proto files. Pass this as
// `includeDirs` to @grpc/proto-loader so imports between rimsky protos resolve.
export const protoDir = join(here, "proto", "v1");

// Absolute path to a named .proto under proto/v1, e.g. protoPath("executor.proto").
export function protoPath(file) {
  return join(protoDir, file);
}
