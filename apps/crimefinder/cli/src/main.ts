#!/usr/bin/env node
import { runPass } from "./commands/pass.js";
import { runStatus } from "./commands/status.js";
import { runUp } from "./commands/up.js";
import { runDown } from "./commands/down.js";

function printUsage(): void {
  console.error("crimefinder <command> [options]");
  console.error("commands:");
  console.error("  pass    --repo <path> --mission <string>");
  console.error("  status  --repo <path>");
  console.error("  up      [--compose-file <path>] [--executor-port N]");
  console.error("  down    [--compose-file <path>]");
}

async function main(): Promise<void> {
  const [, , cmd, ...rest] = process.argv;
  let code = 0;
  switch (cmd) {
    case "pass":
      code = await runPass(rest);
      break;
    case "status":
      code = await runStatus(rest);
      break;
    case "up":
      code = await runUp(rest);
      break;
    case "down":
      code = await runDown(rest);
      break;
    case undefined:
    case "help":
    case "--help":
    case "-h":
      printUsage();
      code = cmd ? 0 : 2;
      break;
    default:
      console.error(`unknown command: ${cmd}`);
      printUsage();
      code = 2;
  }
  process.exit(code);
}

main().catch((err) => {
  console.error(String(err));
  process.exit(1);
});
