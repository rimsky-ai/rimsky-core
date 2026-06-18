// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import * as protoLoader from "@grpc/proto-loader";
import * as grpc from "@grpc/grpc-js";
import { protoDir, protoPath } from "@rimsky-ai/protocols";

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
      includeDirs: [protoDir],
    },
  );
  const pkg = grpc.loadPackageDefinition(
    definition,
  ) as unknown as ExecutorPackage;
  return pkg;
}
