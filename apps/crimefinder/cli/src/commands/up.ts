import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import fs from "node:fs/promises";
import path from "node:path";

const exec = promisify(execFile);

interface UpFlags {
  composeFiles: string[];
  executorPath: string;
  executorPort: number;
  repo: string;
}

async function findRepoRoot(cwd: string): Promise<string> {
  try {
    const { stdout } = await exec("git", ["rev-parse", "--show-toplevel"], { cwd });
    return stdout.trim();
  } catch {
    return cwd;
  }
}

function parseFlags(argv: string[], repoRoot: string): UpFlags {
  const composeFiles: string[] = [];
  let executorPath = path.join(repoRoot, "apps/crimefinder/executor/dist/main.js");
  let executorPort = 7071;
  let repo = process.cwd();
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    if (k === "--compose-file") composeFiles.push(argv[++i]);
    else if (k === "--executor-path") executorPath = argv[++i];
    else if (k === "--executor-port") executorPort = Number(argv[++i]);
    else if (k === "--repo") repo = argv[++i];
  }
  if (composeFiles.length === 0) composeFiles.push("./docker-compose.yml");
  return { composeFiles, executorPath, executorPort, repo };
}

export async function runUp(argv: string[]): Promise<number> {
  const repoRoot = await findRepoRoot(process.cwd());
  const flags = parseFlags(argv, repoRoot);
  const dockerArgs = ["compose", ...flags.composeFiles.flatMap((f) => ["-f", f]), "up", "-d"];
  await exec("docker", dockerArgs);

  const child = spawn("node", [flags.executorPath], {
    env: {
      ...process.env,
      CRIMEFINDER_EXECUTOR_PORT_GRPC: String(flags.executorPort),
    },
    detached: true,
    stdio: "ignore",
  });
  child.unref();

  const runtimeDir = path.join(flags.repo, ".crimefinder", "runtime");
  await fs.mkdir(runtimeDir, { recursive: true });
  await fs.writeFile(path.join(runtimeDir, "executor.pid"), String(child.pid));
  console.log(`crimefinder up: executor pid=${child.pid}, port=${flags.executorPort}`);
  return 0;
}
