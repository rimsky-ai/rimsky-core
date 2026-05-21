import { pino } from "pino";
import { startGrpcServer } from "./server.js";
import { startHealthServer } from "./health.js";
import { createGitOps } from "./git-ops.js";

const env = {
  host: process.env.CRIMEFINDER_PRODUCER_HOST ?? "0.0.0.0",
  grpcPort: Number(process.env.CRIMEFINDER_PRODUCER_PORT_GRPC ?? "9100"),
  httpPort: Number(process.env.CRIMEFINDER_PRODUCER_PORT_HTTP ?? "9101"),
  repoRoot: process.env.CRIMEFINDER_PRODUCER_REPO_ROOT,
  stateEndpointUrl: process.env.CRIMEFINDER_PRODUCER_STATE_ENDPOINT_URL,
  logLevel: process.env.LOG_LEVEL ?? "info",
};

if (!env.repoRoot) {
  console.error("CRIMEFINDER_PRODUCER_REPO_ROOT required");
  process.exit(2);
}
if (!env.stateEndpointUrl) {
  console.error("CRIMEFINDER_PRODUCER_STATE_ENDPOINT_URL required");
  process.exit(2);
}

const logger = pino({ level: env.logLevel });

async function main(): Promise<void> {
  const grpcServer = await startGrpcServer({
    host: env.host,
    port: env.grpcPort,
    repoRoot: env.repoRoot!,
    stateEndpointUrl: env.stateEndpointUrl!,
    logger,
  });
  const health = await startHealthServer({
    repoRoot: env.repoRoot!,
    port: env.httpPort,
    host: env.host,
    git: createGitOps(),
    logger,
  });
  logger.info(
    { grpc: grpcServer.address, health: health.port, repoRoot: env.repoRoot },
    "crimefinder_producer_listening",
  );

  const shutdown = async (sig: string): Promise<void> => {
    logger.info({ sig }, "crimefinder_producer_shutdown");
    await Promise.all([grpcServer.shutdown(), health.shutdown()]);
    process.exit(0);
  };
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));
}

main().catch((err) => {
  logger.error({ err: String(err) }, "crimefinder_producer_fatal");
  process.exit(1);
});
