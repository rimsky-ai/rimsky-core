import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { existsSync } from "node:fs";
import * as protoLoader from "@grpc/proto-loader";
import * as grpc from "@grpc/grpc-js";

export interface ProducerPackage {
  rimsky: {
    v1: {
      ClaimProducer: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
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

function resolveClaimProducerProto(filename: string): string {
  const here = dirname(fileURLToPath(import.meta.url));
  // apps/crimefinder/producer/{src,dist}/ → rimsky-root/protocols/proto/v1/
  // is four levels up; container at /app/producer/dist/ → /app/protocols/
  // is two levels up. Walk a wide candidate set so dev and prod both
  // resolve cleanly without runtime config.
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
  // apps/crimefinder/producer/{src,dist}/ → apps/crimefinder/proto/v1/
  // is two levels up; container layout is identical.
  const candidates = [
    resolve(here, `../../proto/v1/${filename}`),
    resolve(here, `../../../proto/v1/${filename}`),
  ];
  return firstExisting(candidates, filename);
}

export function loadProducerProtos(): ProducerPackage {
  const definition = protoLoader.loadSync(
    [
      resolveClaimProducerProto("claim_producer.proto"),
      resolveCrimefinderStateProto("crimefinder_state.proto"),
    ],
    {
      keepCase: true,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
    },
  );
  const pkg = grpc.loadPackageDefinition(definition) as unknown as ProducerPackage;
  return pkg;
}
