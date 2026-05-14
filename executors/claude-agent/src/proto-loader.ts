// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { existsSync } from "node:fs";
import * as protoLoader from "@grpc/proto-loader";
import * as grpc from "@grpc/grpc-js";

/**
 * Loads `protocols/proto/v1/executor.proto` and
 * `protocols/proto/v1/executor_observability.proto` at runtime via
 * `@grpc/proto-loader`. Both proto files live two directories up from
 * this package (`executors/claude-agent/` → `protocols/proto/v1/`).
 */
export interface ExecutorPackage {
  rimsky: {
    v1: {
      Executor: grpc.ServiceClientConstructor & {
        service: grpc.ServiceDefinition;
      };
      ExecutorObservability: grpc.ServiceClientConstructor & {
        service: grpc.ServiceDefinition;
      };
    };
  };
}

function resolveProtoPath(filename: string): string {
  const here = dirname(fileURLToPath(import.meta.url));
  // dist/ or src/ → executors/claude-agent/ → rimsky-go/ → protocols/proto/v1/
  const candidates = [
    resolve(here, `../../../protocols/proto/v1/${filename}`),
    resolve(here, `../../protocols/proto/v1/${filename}`),
    resolve(here, `../protocols/proto/v1/${filename}`),
  ];
  const found = candidates.find((p) => existsSync(p));
  if (!found) {
    throw new Error(`${filename} not found; tried: ${candidates.join(", ")}`);
  }
  return found;
}

export function loadExecutorProto(): ExecutorPackage {
  const definition = protoLoader.loadSync(
    [
      resolveProtoPath("executor.proto"),
      resolveProtoPath("executor_observability.proto"),
    ],
    {
      keepCase: true,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
    },
  );
  const pkg = grpc.loadPackageDefinition(
    definition,
  ) as unknown as ExecutorPackage;
  return pkg;
}
