import { createLogger } from "./observability.js";
import { startExecutorGrpcServer } from "./server.js";

const env = {
  host: process.env.CRIMEFINDER_EXECUTOR_HOST ?? "127.0.0.1",
  grpcPort: Number(process.env.CRIMEFINDER_EXECUTOR_PORT_GRPC ?? "7071"),
  silenceTimeoutMs: Number(process.env.CRIMEFINDER_EXECUTOR_SILENCE_MS ?? "120000"),
  stubMode: process.env.CRIMEFINDER_EXECUTOR_STUB_MODE === "1",
  anthropicApiKey: process.env.ANTHROPIC_API_KEY ?? "",
  claudeOauthToken: process.env.CLAUDE_CODE_OAUTH_TOKEN ?? "",
  logLevel: process.env.LOG_LEVEL ?? "info",
};

if (!env.stubMode && !env.anthropicApiKey && !env.claudeOauthToken) {
  console.error("ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN required (non-stub mode)");
  process.exit(2);
}

const logger = createLogger({ level: env.logLevel });

async function main(): Promise<void> {
  const cliAuth = env.stubMode
    ? undefined
    : {
        anthropicApiKey: env.anthropicApiKey,
        claudeCodeOauthToken: env.claudeOauthToken,
      };
  const server = await startExecutorGrpcServer({
    host: env.host,
    port: env.grpcPort,
    silenceTimeoutMs: env.silenceTimeoutMs,
    stubMode: env.stubMode,
    cliAuth,
    logger,
  });
  logger.info({ address: server.address, stubMode: env.stubMode }, "crimefinder_executor_listening");
  const shutdown = async (sig: string): Promise<void> => {
    logger.info({ sig }, "crimefinder_executor_shutdown");
    await server.shutdown();
    process.exit(0);
  };
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));
}

main().catch((err) => {
  logger.error({ err: String(err) }, "crimefinder_executor_fatal");
  process.exit(1);
});
