import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import pino from "pino";
import * as grpc from "@grpc/grpc-js";
import { startGrpcServer } from "./server.js";
import { loadProducerProtos } from "./proto-loader.js";

const execFile = promisify(execFileCb);
const logger = pino({ level: "silent" });

async function initRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "x@y"], { cwd: dir });
  await execFile("git", ["config", "user.name", "t"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  await fs.writeFile(path.join(dir, "src.ts"), "x");
  await fs.writeFile(path.join(dir, ".gitignore"), ".crimefinder/\n");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "i"], { cwd: dir });
}

describe("startGrpcServer", () => {
  it("starts, accepts Capabilities and Open, and shuts down cleanly", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-server-"));
    await initRepo(dir);
    const server = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      repoRoot: dir,
      stateEndpointUrl: "127.0.0.1:0",
      logger,
    });
    try {
      const pkg = loadProducerProtos();
      const client = new pkg.rimsky.v1.ClaimProducer(server.address, grpc.credentials.createInsecure());
      const caps = await new Promise<{ supports_split_scope: boolean }>((resolve, reject) => {
        client.Capabilities({}, (err: grpc.ServiceError | null, res: unknown) =>
          err ? reject(err) : resolve(res as { supports_split_scope: boolean }),
        );
      });
      expect(caps.supports_split_scope).toBe(true);

      const open = await new Promise<{ acquired?: unknown; unavailable?: unknown }>(
        (resolve, reject) => {
          client.Open(
            {
              selector: "@pass-state:new&mission=test&trigger=manual",
              claim_id: "c_1",
              producer_name: "crimefinder",
              intent: "rw",
              alias: "pass-state",
              template_id: "tmpl",
              instance_id: "inst",
            },
            (err: grpc.ServiceError | null, res: unknown) =>
              err ? reject(err) : resolve(res as never),
          );
        },
      );
      expect(open.acquired ?? open.unavailable).toBeTruthy();
    } finally {
      await server.shutdown();
    }
  });
});
