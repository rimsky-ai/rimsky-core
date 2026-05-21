import { execFile } from "node:child_process";
import { promisify } from "node:util";

const exec = promisify(execFile);

export interface RegisterResult {
  template_hash: string;
}
export interface InstanceCreateResult {
  instance_id: string;
}

type RimskyExec = (program: string, args: string[]) => Promise<{ stdout: string; stderr: string }>;

const defaultExec: RimskyExec = async (program, args) => {
  return exec(program, args);
};

export class RimskyCli {
  private readonly rimskyBin: string;
  private readonly exec: RimskyExec;

  constructor(opts: { rimskyBin?: string; exec?: RimskyExec } = {}) {
    this.rimskyBin = opts.rimskyBin ?? "rimsky";
    this.exec = opts.exec ?? defaultExec;
  }

  async templateRegister(yamlPath: string, opts: { tag?: string } = {}): Promise<RegisterResult> {
    const args = ["template", "register", yamlPath, "--format", "json"];
    if (opts.tag) args.push("--tag", opts.tag);
    const { stdout } = await this.exec(this.rimskyBin, args);
    return JSON.parse(stdout) as RegisterResult;
  }

  async templateDeploy(hashOrTag: string): Promise<void> {
    await this.exec(this.rimskyBin, ["template", "deploy", hashOrTag]);
  }

  async instanceCreate(
    template: string,
    params: Record<string, unknown>,
  ): Promise<InstanceCreateResult> {
    const { stdout } = await this.exec(this.rimskyBin, [
      "instance",
      "create",
      "--template",
      template,
      "--params",
      JSON.stringify(params),
      "--format",
      "json",
    ]);
    return JSON.parse(stdout) as InstanceCreateResult;
  }

  async instanceGet(instanceId: string): Promise<unknown> {
    const { stdout } = await this.exec(this.rimskyBin, [
      "instance",
      "get",
      instanceId,
      "--format",
      "json",
    ]);
    return JSON.parse(stdout);
  }

  // Lists active (non-terminal) instances scoped to a template. Returns
  // []], not an error, when rimsky isn't reachable — the status command
  // displays JSONL history regardless of whether the control-api is up.
  async instanceList(opts: { template?: string } = {}): Promise<Array<Record<string, unknown>>> {
    const args = ["instance", "list", "--format", "json"];
    if (opts.template) args.push("--template", opts.template);
    try {
      const { stdout } = await this.exec(this.rimskyBin, args);
      const parsed = JSON.parse(stdout);
      if (Array.isArray(parsed)) return parsed as Array<Record<string, unknown>>;
      return [];
    } catch {
      return [];
    }
  }
}
