import fs from "node:fs";
import yaml from "yaml";
const paths = process.argv.slice(2);
if (paths.length === 0) {
  console.error("usage: validate-yaml.mjs <file>...");
  process.exit(2);
}
let failed = false;
for (const p of paths) {
  try {
    yaml.parse(fs.readFileSync(p, "utf-8"));
    console.log(`${p}: OK`);
  } catch (e) {
    console.error(`${p}: ${e.message}`);
    failed = true;
  }
}
process.exit(failed ? 1 : 0);
