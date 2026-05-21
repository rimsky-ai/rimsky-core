import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { existsSync } from "node:fs";
import * as protoLoader from "@grpc/proto-loader";
import * as grpc from "@grpc/grpc-js";

export interface ExecutorPackage {
  rimsky: {
    v1: {
      Executor: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
      ExecutorObservability: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
    };
  };
  crimefinder: {
    v1: {
      CrimefinderState: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
    };
  };
}

function firstExisting(candidates: string[], filename: string): string {
  const found = candidates.find((p) => existsSync(p));
  if (!found) {
    throw new Error(`${filename} not found; tried:\n  ${candidates.join("\n  ")}`);
  }
  return found;
}

function resolveProtocolsProto(filename: string): string {
  const here = dirname(fileURLToPath(import.meta.url));
  const candidates = [
    resolve(here, `../../../../protocols/proto/v1/${filename}`),
    resolve(here, `../../../protocols/proto/v1/${filename}`),
    resolve(here, `../../protocols/proto/v1/${filename}`),
    resolve(here, `../protocols/proto/v1/${filename}`),
  ];
  return firstExisting(candidates, filename);
}

function resolveCrimefinderStateProto(filename: string): string {
  const here = dirname(fileURLToPath(import.meta.url));
  const candidates = [
    resolve(here, `../../proto/v1/${filename}`),
    resolve(here, `../../../proto/v1/${filename}`),
  ];
  return firstExisting(candidates, filename);
}

export function loadExecutorProtos(): ExecutorPackage {
  const def = protoLoader.loadSync(
    [
      resolveProtocolsProto("executor.proto"),
      resolveProtocolsProto("executor_observability.proto"),
      resolveCrimefinderStateProto("crimefinder_state.proto"),
    ],
    { keepCase: true, longs: String, enums: String, defaults: true, oneofs: true },
  );
  return grpc.loadPackageDefinition(def) as unknown as ExecutorPackage;
}
