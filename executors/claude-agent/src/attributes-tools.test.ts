// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import * as http from "node:http";
import type { AddressInfo } from "node:net";
import {
  buildAttributesWritebackUrl,
  defaultPostAttributes,
  AttributesReadInput,
  AttributesSetInput,
  ATTRIBUTES_TOOL_DEFINITIONS,
} from "./attributes-tools.js";

describe("buildAttributesWritebackUrl", () => {
  it("appends /v1/attributes/{node_id} with URL-encoded node id", () => {
    const url = buildAttributesWritebackUrl(
      "http://supervisor.invalid",
      "n/1",
    );
    expect(url).toBe(
      "http://supervisor.invalid/v1/attributes/n%2F1",
    );
  });

  it("strips trailing slashes from base", () => {
    const url = buildAttributesWritebackUrl(
      "http://supervisor.invalid///",
      "n-1",
    );
    expect(url).toBe("http://supervisor.invalid/v1/attributes/n-1");
  });
});

describe("defaultPostAttributes", () => {
  // Spec §12.5: POSTs `{delta}` with the cancel token as bearer auth, body
  // shaped exactly `{delta: {...}}`. End-to-end against a fake supervisor.
  let server: http.Server;
  let received: {
    path: string;
    headers: Record<string, string | string[] | undefined>;
    body: unknown;
  } | null = null;
  let serverUrl: string;
  let nextStatus = 204;

  beforeEach(async () => {
    received = null;
    nextStatus = 204;
    server = http.createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        try {
          received = {
            path: req.url ?? "",
            headers: { ...req.headers },
            body: JSON.parse(Buffer.concat(chunks).toString("utf8")),
          };
        } catch {
          received = {
            path: req.url ?? "",
            headers: { ...req.headers },
            body: null,
          };
        }
        res.statusCode = nextStatus;
        res.end();
      });
    });
    await new Promise<void>((resolve) =>
      server.listen(0, "127.0.0.1", () => resolve()),
    );
    const addr = server.address() as AddressInfo;
    serverUrl = `http://127.0.0.1:${addr.port}`;
  });

  afterEach(async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });

  it("POSTs delta body with cancel-token in Authorization header", async () => {
    const url = buildAttributesWritebackUrl(serverUrl, "node-7");
    const result = await defaultPostAttributes(
      url,
      { delta: { progress: "halfway", count: 3 } },
      "supervisor-issued-cancel-token",
    );
    expect(result.status).toBe(204);
    expect(received).not.toBeNull();
    expect(received!.path).toBe("/v1/attributes/node-7");
    expect(received!.headers.authorization).toBe(
      "Bearer supervisor-issued-cancel-token",
    );
    expect(received!.body).toEqual({
      delta: { progress: "halfway", count: 3 },
    });
  });

  it("propagates non-2xx status without throwing", async () => {
    nextStatus = 422;
    const url = buildAttributesWritebackUrl(serverUrl, "node-x");
    const result = await defaultPostAttributes(
      url,
      { delta: { x: 1 } },
      "ct",
    );
    expect(result.status).toBe(422);
  });
});

describe("attributes-tool input schemas", () => {
  it("AttributesReadInput parses minimal payload", () => {
    expect(AttributesReadInput.parse({ token: "tok" }).token).toBe("tok");
    expect(() => AttributesReadInput.parse({})).toThrow();
  });

  it("AttributesSetInput requires a record-shaped delta", () => {
    expect(
      AttributesSetInput.parse({ token: "tok", delta: { ok: 1 } }).delta,
    ).toEqual({ ok: 1 });
    expect(() =>
      AttributesSetInput.parse({ token: "tok", delta: 12 }),
    ).toThrow();
  });

  it("ATTRIBUTES_TOOL_DEFINITIONS exposes read + set", () => {
    const names = ATTRIBUTES_TOOL_DEFINITIONS.map((t) => t.name).sort();
    expect(names).toEqual(["attributes_read", "attributes_set"]);
  });
});
