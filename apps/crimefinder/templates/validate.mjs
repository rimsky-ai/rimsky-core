import fs from "node:fs";
import yaml from "yaml";

const doc = yaml.parse(fs.readFileSync(process.argv[2], "utf-8"));
const errors = [];

// Cross-graph node-type uniqueness.
const typeOrigin = new Map();
const flag = (graphName, type) => {
  const prior = typeOrigin.get(type);
  if (prior) {
    errors.push(`duplicate node type "${type}": appears in both ${prior} and ${graphName}`);
  } else {
    typeOrigin.set(type, graphName);
  }
};
for (const n of doc.nodes ?? []) flag("main", n.type);
for (const g of doc.graphs ?? []) {
  for (const n of g.nodes ?? []) flag(g.name, n.type);
}

// Every crimefinder node carries tags:.
const checkTags = (graphName, n) => {
  if (!Array.isArray(n.tags) || n.tags.length === 0) {
    errors.push(`${graphName}: node ${n.type} missing tags:`);
  }
};
for (const n of doc.nodes ?? []) checkTags("main", n);
for (const g of doc.graphs ?? []) {
  for (const n of g.nodes ?? []) checkTags(`sub-graph ${g.name}`, n);
}

// Sub-graph encapsulation: holds:/subscribes: reference only internal
// nodes; selector and partition_request substitutions of the form
// `{{nodes.X.attribute.Y}}` reference only internal-or-entry nodes.
// Rimsky's runtime parser allows cross-graph references for nodes
// absorbed via the concept:delegation pattern, so we LOG these as
// warnings rather than errors — escape hatch for legitimate sub-graph
// boundaries crossing.
function findNodeRefsInString(s) {
  const matches = [];
  const re = /\{\{\s*nodes\.([A-Za-z0-9_-]+)\./g;
  let m;
  while ((m = re.exec(s)) !== null) matches.push(m[1]);
  return matches;
}

function scanForRefs(obj, refs) {
  if (typeof obj === "string") {
    for (const r of findNodeRefsInString(obj)) refs.push(r);
    return;
  }
  if (Array.isArray(obj)) {
    for (const v of obj) scanForRefs(v, refs);
    return;
  }
  if (obj && typeof obj === "object") {
    for (const v of Object.values(obj)) scanForRefs(v, refs);
  }
}

for (const g of doc.graphs ?? []) {
  // The `main` graph carries the template's top-level nodes and must
  // NOT declare entry/exit (rimsky's canonicalizer rejects
  // `subgraph_main_has_entry_or_exit`). Only sub-graphs require them.
  if (g.name !== "main" && (!g.entry || !g.exit)) {
    errors.push(`sub-graph ${g.name} missing entry/exit`);
  }
  const internalTypes = new Set((g.nodes ?? []).map((n) => n.type));
  if (g.entry) internalTypes.add(g.entry);
  for (const n of g.nodes ?? []) {
    for (const sub of n.subscribes ?? []) {
      if (sub.node && !internalTypes.has(sub.node)) {
        errors.push(`sub-graph ${g.name}: node ${n.type} subscribes to non-internal node ${sub.node}`);
      }
    }
    for (const [alias, h] of Object.entries(n.holds ?? {})) {
      if (h.from && !internalTypes.has(h.from)) {
        errors.push(`sub-graph ${g.name}: node ${n.type} holds ${alias} from non-internal node ${h.from}`);
      }
    }
    // Scan selector and partition_request fields for `{{nodes.X.…}}`
    // references and check each X against internalTypes.
    const refs = [];
    if (typeof n.selector === "string") scanForRefs(n.selector, refs);
    if (n.partition_request) scanForRefs(n.partition_request, refs);
    for (const r of refs) {
      if (!internalTypes.has(r)) {
        console.warn(
          `sub-graph ${g.name}: node ${n.type} substitutes ` +
            `{{nodes.${r}.…}} (not internal-or-entry); ` +
            `verify against rimsky's runtime resolver (concept:delegation absorption may permit this)`,
        );
      }
    }
  }
}

if (errors.length) {
  errors.forEach((e) => console.error(e));
  process.exit(1);
}
console.log("template OK");
