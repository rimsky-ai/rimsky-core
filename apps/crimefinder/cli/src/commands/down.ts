import { execFile } from "node:child_process";
import { promisify } from "node:util";
import fs from "node:fs/promises";
import path from "node:path";

const exec = promisify(execFile);

interface DownFlags {
  composeFiles: string[];
  repo: string;
}

function parseFlags(argv: string[]): DownFlags {
  const composeFiles: string[] = [];
  let repo = process.cwd();
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    if (k === "--compose-file") composeFiles.push(argv[++i]);
    else if (k === "--repo") repo = argv[++i];
  }
  if (composeFiles.length === 0) composeFiles.push("./docker-compose.yml");
  return { composeFiles, repo };
}

export async function runDown(argv: string[]): Promise<number> {
  const flags = parseFlags(argv);
  const dockerArgs = ["compose", ...flags.composeFiles.flatMap((f) => ["-f", f]), "down"];
  await exec("docker", dockerArgs).catch(() => undefined);

  const pidPath = path.join(flags.repo, ".crimefinder", "runtime", "executor.pid");
  try {
    const pidStr = (await fs.readFile(pidPath, "utf-8")).trim();
    const pid = Number(pidStr);
    if (Number.isFinite(pid)) {
      try {
        process.kill(pid, "SIGTERM");
        console.log(`crimefinder down: signaled executor pid=${pid}`);
      } catch {
        console.warn(`crimefinder down: executor pid=${pid} not running`);
      }
    }
    await fs.unlink(pidPath).catch(() => undefined);
  } catch {
    console.warn("crimefinder down: no executor.pid file; only ran docker compose down");
  }
  return 0;
}
