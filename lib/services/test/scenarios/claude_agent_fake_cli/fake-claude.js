#!/usr/bin/env node
// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
const resumeToken = readArg("--resume") ?? "";

const callbackUrl = process.env.RIMSKY_CALLBACK_URL ?? "";
const callbackToken = process.env.RIMSKY_CALLBACK_TOKEN ?? "";
const SIGNOFF_PRIVATE_KEY_PATH =
  "/etc/rimsky/fake-claude-signoff-private-key.pem";
let signoffPrivateKeyPem = "";
try {
  signoffPrivateKeyPem = fs.readFileSync(SIGNOFF_PRIVATE_KEY_PATH, "utf8");
} catch {
}
const WITNESS_PATH = "/tmp/fake-claude-validator-header.txt";

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
if (sessionId === "" && resumeToken === "") {
  fail("either --session-id (spawn) or --resume (resume) is required");
}

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
  const sid = res.headers.get("mcp-session-id");
  if (sid && !mcpSessionId) mcpSessionId = sid;
  const text = await res.text();
  if (!res.ok) {
    fail(`MCP ${method} -> HTTP ${res.status}: ${text}`);
  }
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
  const sig = signValue(endpoints);
  const delta = { endpoints };
  await callTool("report_complete", {
    changed: true,
    attributes_delta: delta,
    signoffs: [sig],
  });
}

async function scenarioSignoffMissing() {
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
  try {
    fs.writeFileSync(WITNESS_PATH, resolvedHeader);
  } catch (e) {
    process.stderr.write(`fake-claude: witness write failed: ${e}\n`);
  }
  const signedValue = {
    validator_header_digest_sha256: digest,
    scenario: "env_ref_witness",
  };
  const sig = signValue(signedValue);
  await callTool("report_complete", {
    changed: true,
    attributes_delta: {
      cli_observation: {
        validator_header_digest_sha256: digest,
        scenario: "env_ref_witness",
      },
    },
    signoffs: [sig],
  });
}

function chainIDFromPrompt(prompt) {
  const m = prompt.match(/scenario:session_resume:([A-Za-z0-9_-]+)/);
  return m ? m[1] : "default";
}

function sessionResumeLogPath(chainID) {
  return `/tmp/fake-claude-session-resume-${chainID}.json`;
}

function readSessionResumeLog(chainID) {
  try {
    const raw = fs.readFileSync(sessionResumeLogPath(chainID), "utf8");
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function writeSessionResumeLog(chainID, state) {
  fs.writeFileSync(sessionResumeLogPath(chainID), JSON.stringify(state));
}

async function scenarioSessionResume() {
  const chainID = chainIDFromPrompt(userPrompt);
  let turn;
  let priorRecall;
  if (resumeToken === "") {
    turn = 1;
    priorRecall = "";
    writeSessionResumeLog(chainID, { turn: 1, name: "Alpha" });
  } else {
    const prior = readSessionResumeLog(chainID);
    if (!prior || typeof prior.turn !== "number") {
      fail(
        `scenario:session_resume: --resume ${resumeToken} but no prior conversation log at ${sessionResumeLogPath(chainID)} — ` +
          "the executor passed --resume on what should have been a fresh dispatch",
      );
    }
    turn = prior.turn + 1;
    priorRecall = String(prior.name ?? "");
    writeSessionResumeLog(chainID, { turn, name: priorRecall });
  }

  await callTool("report_complete", {
    changed: true,
    attributes_delta: {
      fake_cli_turn: turn,
      fake_cli_prior_recall: priorRecall,
      fake_cli_resumed_with: resumeToken,
    },
  });
}

async function scenarioDispatchContextProbe() {
  const result = await callTool("dispatch_context_read", {});
  const arr = result.content;
  const observed = JSON.parse(arr[0].text);
  await callTool("report_complete", {
    changed: false,
    attributes_delta: { dispatch_context: observed },
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
  } else if (userPrompt.includes("scenario:session_resume")) {
    await scenarioSessionResume();
  } else if (userPrompt.includes("scenario:dispatch_context_probe")) {
    await scenarioDispatchContextProbe();
  } else {
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
