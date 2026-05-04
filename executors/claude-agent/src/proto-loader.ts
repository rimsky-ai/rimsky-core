import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { existsSync } from "node:fs";
import * as protoLoader from "@grpc/proto-loader";
import * as grpc from "@grpc/grpc-js";

/**
 * Loads `protocols/proto/v1/executor.proto` at runtime via `@grpc/proto-loader`.
 * The proto file lives two directories up from this package
 * (`executors/claude-agent/` → `protocols/proto/v1/`).
 */
export interface NodeExecutorPackage {
  rimsky: {
    v1: {
      NodeExecutor: grpc.ServiceClientConstructor & {
        service: grpc.ServiceDefinition;
      };
    };
  };
}

export function loadNodeExecutorProto(): NodeExecutorPackage {
  const here = dirname(fileURLToPath(import.meta.url));
  // dist/ or src/ → executors/claude-agent/ → rimsky-go/ → protocols/proto/v1/
  const candidates = [
    resolve(here, "../../../protocols/proto/v1/executor.proto"),
    resolve(here, "../../protocols/proto/v1/executor.proto"),
    resolve(here, "../protocols/proto/v1/executor.proto"),
  ];
  const protoPath = candidates.find((p) => existsSync(p));
  if (!protoPath) {
    throw new Error(
      `executor.proto not found; tried: ${candidates.join(", ")}`,
    );
  }
  const definition = protoLoader.loadSync(protoPath, {
    keepCase: true,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
  });
  const pkg = grpc.loadPackageDefinition(
    definition,
  ) as unknown as NodeExecutorPackage;
  return pkg;
}
