import path from "node:path";
import { fileURLToPath } from "node:url";
import { RimskyCli } from "../rimsky-cli.js";

const here = path.dirname(fileURLToPath(import.meta.url));

export interface PassArgs {
  repo: string;
  mission: string;
  templatePath: string;
  cli?: RimskyCli;
}

function parseFlags(argv: string[]): { mission: string; repo: string; templatePath: string } {
  let mission = "convergence pass";
  let repo = process.cwd();
  let templatePath = path.resolve(here, "..", "..", "..", "templates", "code-review-pass.yml");
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    if (k === "--mission") mission = argv[++i];
    else if (k === "--repo") repo = argv[++i];
    else if (k === "--template") templatePath = argv[++i];
  }
  return { mission, repo, templatePath };
}

export async function runPass(argv: string[]): Promise<number> {
  const flags = parseFlags(argv);
  const cli = new RimskyCli();
  const reg = await cli.templateRegister(flags.templatePath, { tag: "crimefinder-code-review-pass" });
  const inst = await cli.instanceCreate(reg.template_hash, {
    repo_root: path.resolve(flags.repo),
    mission: flags.mission,
    trigger: "manual",
  });
  console.log(`pass started: instance_id=${inst.instance_id}`);
  return 0;
}
