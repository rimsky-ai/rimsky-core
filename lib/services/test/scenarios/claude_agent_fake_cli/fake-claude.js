#!/usr/bin/env node
// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// fake-claude.js — stub Claude CLI for the claude-agent cross-stack proof.
//
// The real Claude CLI's role in a claude-agent dispatch is:
//   1. Receive --session-id, --mcp-config, -p (user prompt), and a small set
//      of bookkeeping flags from the executor.
//   2. Speak MCP over the rimsky-callback transport (a streamable-HTTP MCP
//      server the executor stands up on a per-dispatch loopback URL).
//   3. Eventually call exactly one terminal MCP tool: report_complete /
//      report_error / report_blocked / report_park. The token argument is
//      the per-dispatch callback token the executor pre-allocated.
//
// This stub plays the same role with a scripted protocol, branched on the
// scenario hint embedded in the user prompt. It does NOT replace the
// executor itself — the executor's CLI runner spawns this script the
// same way it would spawn the real binary; the gate, the writeback path,
// the async-callback dispatch, and the rimsky supervisor on the other
// side are all real.
//
// The stub exists so the cross-stack proof can drive the claude-agent
// dispatch path end-to-end in CI without the third-party Claude CLI
// (credentials + cost + flakiness on rate limits).
//
// Scenario hints in the user prompt:
//   - "scenario:signoff_ok"        → real Ed25519 signature over the bound
//                                    output, then report_complete (success)
//   - "scenario:signoff_missing"   → report_complete WITHOUT a signoffs
//                                    array, expected to be rejected by the
//                                    sign-off gate
//   - "scenario:rate_limited"      → report_error(error_class:
//                                    "agent/rate_limited")
//   - "scenario:env_ref_witness"   → read the spawn's --mcp-config file,
//                                    record (a) the resolved validator
//                                    Authorization header (proving the
//                                    executor resolved ${env:VAR} at
//                                    spawn) and (b) the SHA-256 of the
//                                    resolved value into the writeback,
//                                    then report_complete with a signed
//                                    delta. The rimsky-side persisted
//                                    attribute bag is then asserted to
//                                    carry the digest but never the
//                                    plaintext.

import fs from "node:fs";
import crypto from "node:crypto";

const argv = process.argv.slice(2);

function readArg(name) {
  const i = argv.indexOf(name);
  if (i < 0 || i + 1 >= argv.length) return null;
  return argv[i + 1];
}

const sessionId = readArg("--session-id") ?? "";
const mcpConfigPath = readArg("--mcp-config") ?? "";
const userPrompt = readArg("-p") ?? "";

const callbackUrl = process.env.RIMSKY_CALLBACK_URL ?? "";
const callbackToken = process.env.RIMSKY_CALLBACK_TOKEN ?? "";
// The signoff private key lives in a known on-disk path (mounted by the
// Go harness via testcontainers' WithFiles). The executor's cli-runner.ts
// scrubs the parent process.env from the spawned subprocess, leaving only
// the per-spawn `req.env` (RIMSKY_CALLBACK_*) and the auth env. The PEM
// is read directly from disk so a host-scoped env var the harness sets on
// the executor container is NOT a requirement for the stub.
const SIGNOFF_PRIVATE_KEY_PATH =
  "/etc/rimsky/fake-claude-signoff-private-key.pem";
let signoffPrivateKeyPem = "";
try {
  signoffPrivateKeyPem = fs.readFileSync(SIGNOFF_PRIVATE_KEY_PATH, "utf8");
} catch {
  // The not-signoff scenarios (rate-limited / inline-refused) never reach
  // signValue(), so an absent PEM is benign there. The signoff branches
  // fail loud below when the value is empty.
}
// Witness file path: the env-ref scenario reads --mcp-config and (if a
// witness path is configured at a fixed location) writes the resolved
// Authorization header there so the host test can read the plaintext
// from outside the container. Like the PEM, this is a known on-disk path
// (rather than an env var) so the executor's env-scrubbing doesn't break
// the witness flow.
const WITNESS_PATH = "/tmp/fake-claude-validator-header.txt";

// Emit a couple of NDJSON lines on stdout so the executor's silence-
// tracker resets — the real CLI emits stream-json continuously, and a
// stub that's silent for >silenceTimeoutMs trips the silence terminal
// before our MCP callback lands.
process.stdout.write(
  JSON.stringify({ type: "system", subtype: "fake-claude", session_id: sessionId }) +
    "\n",
);

function fail(msg) {
  process.stderr.write(`fake-claude: ${msg}\n`);
  process.exit(2);
}

if (callbackUrl === "") fail("RIMSKY_CALLBACK_URL is unset");
if (callbackToken === "") fail("RIMSKY_CALLBACK_TOKEN is unset");
if (sessionId === "") fail("--session-id missing");

// Minimal MCP client over streamable-HTTP / JSON-RPC. We use a single
// initialize + tools/call exchange, no SSE — the rimsky-callback server's
// tool replies are returned inline on the POST response. We hand-roll the
// JSON-RPC framing instead of pulling @modelcontextprotocol/sdk so the
// stub stays self-contained (the production claude-agent node_modules in
// the runtime image does not include the MCP SDK).

let nextId = 1;
let mcpSessionId = null;

async function mcpRequest(method, params) {
  const id = nextId++;
  const body = JSON.stringify({
    jsonrpc: "2.0",
    id,
    method,
    params: params ?? {},
  });
  const headers = {
    "Content-Type": "application/json",
    Accept: "application/json, text/event-stream",
  };
  if (mcpSessionId) headers["Mcp-Session-Id"] = mcpSessionId;
  const res = await fetch(callbackUrl, {
    method: "POST",
    headers,
    body,
  });
  // The streamable-HTTP server may assign a session id on initialize.
  const sid = res.headers.get("mcp-session-id");
  if (sid && !mcpSessionId) mcpSessionId = sid;
  const text = await res.text();
  if (!res.ok) {
    fail(`MCP ${method} -> HTTP ${res.status}: ${text}`);
  }
  // Streamable-HTTP servers may reply as SSE event stream when the client
  // accepts text/event-stream. Detect the SSE wrapper and extract the
  // `data:` JSON payload; otherwise parse as direct JSON.
  if (
    res.headers.get("content-type")?.startsWith("text/event-stream") ||
    text.startsWith("event:") ||
    text.includes("\ndata:")
  ) {
    const dataLines = text
      .split(/\r?\n/)
      .filter((l) => l.startsWith("data:"))
      .map((l) => l.slice("data:".length).trim());
    if (dataLines.length === 0) {
      fail(`MCP ${method} returned no SSE data lines: ${text}`);
    }
    // The terminal response for an id we issued is the last data line
    // that decodes to a JSON-RPC envelope carrying our id; sticking to
    // the last one is safe for these one-shot tool calls.
    const last = dataLines[dataLines.length - 1];
    return JSON.parse(last);
  }
  return JSON.parse(text);
}

async function initializeMcp() {
  const res = await mcpRequest("initialize", {
    protocolVersion: "2025-06-18",
    capabilities: {},
    clientInfo: { name: "rimsky-fake-claude", version: "1.0.0" },
  });
  if (res.error) fail(`MCP initialize error: ${JSON.stringify(res.error)}`);
  // initialized notification is best-effort.
  try {
    const headers = {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    };
    if (mcpSessionId) headers["Mcp-Session-Id"] = mcpSessionId;
    await fetch(callbackUrl, {
      method: "POST",
      headers,
      body: JSON.stringify({
        jsonrpc: "2.0",
        method: "notifications/initialized",
        params: {},
      }),
    });
  } catch {
    /* notification is fire-and-forget */
  }
}

async function callTool(name, args) {
  const res = await mcpRequest("tools/call", {
    name,
    arguments: { token: callbackToken, ...args },
  });
  if (res.error) fail(`MCP tools/call ${name} error: ${JSON.stringify(res.error)}`);
  return res.result;
}

function loadMcpServers() {
  if (!mcpConfigPath) return {};
  try {
    const raw = fs.readFileSync(mcpConfigPath, "utf8");
    const parsed = JSON.parse(raw);
    return parsed.mcpServers ?? {};
  } catch (e) {
    fail(`could not read --mcp-config at ${mcpConfigPath}: ${e}`);
  }
}

// Canonicalize per RFC 8785 for the signoff message. We hand-roll a tiny
// canonicalizer matching `canonicalize` (the dependency the executor's
// signoff.ts uses) for primitives + objects + arrays — sufficient for
// the value shapes this proof binds (a flat object with string fields).
function canonicalize(v) {
  if (v === null || typeof v !== "object") return JSON.stringify(v);
  if (Array.isArray(v)) {
    return "[" + v.map(canonicalize).join(",") + "]";
  }
  const keys = Object.keys(v).sort();
  return (
    "{" +
    keys
      .map((k) => JSON.stringify(k) + ":" + canonicalize(v[k]))
      .join(",") +
    "}"
  );
}

const SIGNOFF_DOMAIN = "rimsky/claude-agent/signoff/v1";

function signValue(value) {
  if (!signoffPrivateKeyPem) {
    fail("RIMSKY_FAKE_CLAUDE_SIGNOFF_PRIVATE_KEY unset; cannot sign");
  }
  const msg = Buffer.from(
    `${SIGNOFF_DOMAIN}\n${sessionId}\n${canonicalize(value)}`,
    "utf8",
  );
  const sig = crypto.sign(null, msg, signoffPrivateKeyPem);
  return sig.toString("base64");
}

async function scenarioSignoffOK() {
  const endpoints = [{ url: "https://verified.example/projectalpha/run1" }];
  // signoffs is an array of bare base64 signature strings (per the
  // ReportCompleteInput Zod schema in internal-mcp-tools.ts); the gate
  // tries every signature against every required `(public_key, path)`,
  // picking the one that verifies for each.
  const sig = signValue(endpoints);
  const delta = { endpoints };
  await callTool("report_complete", {
    changed: true,
    attributes_delta: delta,
    signoffs: [sig],
  });
}

async function scenarioSignoffMissing() {
  // Deliberately omit `signoffs` so the gate rejects each attempt;
  // after max_signoff_attempts the executor emits
  // agent/signoff_unobtained terminal.
  const endpoints = [{ url: "https://verified.example/projectalpha/run-bad" }];
  for (let attempt = 0; attempt < 10; attempt++) {
    await callTool("report_complete", {
      changed: true,
      attributes_delta: { endpoints },
    });
  }
}

async function scenarioRateLimited() {
  await callTool("report_error", {
    error_class: "agent/rate_limited",
    payload: { reason: "fake-claude scripted upstream 429" },
  });
}

async function scenarioEnvRefWitness() {
  // The witness path is two-pronged:
  //   1. Read the spawn's --mcp-config and locate the validator server's
  //      headers. The executor resolves ${env:VAR} in those headers at
  //      spawn (S-executors-validator-header-secret-refs), so the value
  //      we read here is the *resolved* plaintext token — exactly what
  //      must NOT appear in rimsky-persisted node attributes.
  //   2. Record the SHA-256 of the resolved value in attributes_set,
  //      not the value itself. The rimsky-side assertion then queries
  //      the persisted node attribute bag for that digest (proving the
  //      writeback happened) AND scans the entire payload for the
  //      plaintext (proving it did NOT leak).
  const servers = loadMcpServers();
  const validator = servers["validator"];
  if (!validator || !validator.headers || !validator.headers.Authorization) {
    fail(
      "scenario:env_ref_witness: validator server missing or has no Authorization header in --mcp-config — " +
        "the executor's spawn-time ${env:} resolution did not reach the spawn argv",
    );
  }
  const resolvedHeader = String(validator.headers.Authorization);
  const digest = crypto
    .createHash("sha256")
    .update(resolvedHeader, "utf8")
    .digest("hex");
  // Also write the resolved header (plaintext) to a witness file the
  // host test can read via docker exec — this proves the executor did
  // resolve the env ref before spawning the CLI, distinct from the
  // proof that the plaintext is absent from rimsky-persisted state.
  try {
    fs.writeFileSync(WITNESS_PATH, resolvedHeader);
  } catch (e) {
    // Witness file failure does not abort the dispatch — the rimsky-
    // side absent-plaintext check is the primary acceptance.
    process.stderr.write(`fake-claude: witness write failed: ${e}\n`);
  }
  // Incremental writeback so the supervisor records the digest as a
  // committed attribute. report_complete then carries no attributes_delta
  // (the bound value lives in the writeback alone).
  await callTool("attributes_set", {
    delta: {
      cli_observation: {
        validator_header_digest_sha256: digest,
        scenario: "env_ref_witness",
      },
    },
  });
  // Sign the value at the configured signoff path (cli_observation) so
  // the dispatch passes the gate.
  const signedValue = {
    validator_header_digest_sha256: digest,
    scenario: "env_ref_witness",
  };
  const sig = signValue(signedValue);
  await callTool("report_complete", {
    changed: true,
    signoffs: [sig],
  });
}

async function main() {
  await initializeMcp();
  if (userPrompt.includes("scenario:signoff_ok")) {
    await scenarioSignoffOK();
  } else if (userPrompt.includes("scenario:signoff_missing")) {
    await scenarioSignoffMissing();
  } else if (userPrompt.includes("scenario:rate_limited")) {
    await scenarioRateLimited();
  } else if (userPrompt.includes("scenario:env_ref_witness")) {
    await scenarioEnvRefWitness();
  } else {
    // Default: a vanilla success so accidental probes don't dangle.
    await callTool("report_complete", {
      changed: true,
      attributes_delta: { stub: true },
    });
  }
  process.exit(0);
}

main().catch((e) => {
  process.stderr.write(`fake-claude: ${e?.stack ?? e}\n`);
  process.exit(1);
});
