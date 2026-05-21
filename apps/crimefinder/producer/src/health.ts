import path from "node:path";
import fs from "node:fs/promises";
import Fastify, { FastifyInstance } from "fastify";
import type { Logger } from "pino";
import type { GitOps } from "./git-ops.js";

export interface HealthOptions {
  repoRoot: string;
  port: number;
  host?: string;
  git: GitOps;
  logger: Logger;
}

export interface HealthHandle {
  shutdown(): Promise<void>;
  port: number;
  app: FastifyInstance;
}

// Readiness signal: the producer is healthy iff it can mkdir the
// `.crimefinder/` directory and `git status` against the repo. We don't
// write a touch file — it accumulates write traffic for every probe.
// The mkdir is idempotent; the git call exercises the GitOps surface.
export async function startHealthServer(opts: HealthOptions): Promise<HealthHandle> {
  const app = Fastify({ logger: false });

  app.get("/health", async (_req, reply) => {
    try {
      const dir = path.join(opts.repoRoot, ".crimefinder");
      await fs.mkdir(dir, { recursive: true });
      await opts.git.status(opts.repoRoot);
      return reply.code(200).send({ status: "ok" });
    } catch (e) {
      opts.logger.warn({ err: String(e) }, "health_check_failed");
      return reply.code(503).send({ status: "error", error: String(e) });
    }
  });

  await app.listen({ port: opts.port, host: opts.host ?? "0.0.0.0" });
  const addr = app.server.address();
  const port = typeof addr === "object" && addr ? addr.port : opts.port;
  return {
    port,
    app,
    async shutdown() {
      await app.close();
    },
  };
}
