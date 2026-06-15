// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import * as protoLoader from "@grpc/proto-loader";
import * as grpc from "@grpc/grpc-js";
import { protoDir, protoPath } from "@rimsky-ai/protocols";

/**
 * Loads `executor.proto` and `executor_observability.proto` at runtime via
 * `@grpc/proto-loader`. The `.proto` definitions ship in the
 * `@rimsky-ai/protocols` package (the rimsky-core wire contract published to
 * npm); `protoPath`/`protoDir` resolve their on-disk location inside the
 * installed package so we never hardcode a node_modules path.
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

export function loadExecutorProto(): ExecutorPackage {
  const definition = protoLoader.loadSync(
    [protoPath("executor.proto"), protoPath("executor_observability.proto")],
    {
      keepCase: true,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      // @deliberate: resolve any cross-proto imports against the package's proto dir.
      includeDirs: [protoDir],
    },
  );
  const pkg = grpc.loadPackageDefinition(
    definition,
  ) as unknown as ExecutorPackage;
  return pkg;
}
