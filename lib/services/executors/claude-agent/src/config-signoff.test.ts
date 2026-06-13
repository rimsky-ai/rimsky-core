// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { parseCliConfig as parseCliConfigServer } from "./server.js";
import { parseCliConfig as parseCliConfigBridge } from "./http-bridge.js";
import {
  declaredErrorClasses,
  expectedAttributesSchema,
} from "./expected-attributes-schema.js";
import { CliConfigError } from "./cli-config-error.js";

// The gRPC server and the HTTP bridge each carry a copy of parseCliConfig
// (the bridge's is `@source: lib/services/executors/claude-agent/src/server.ts`). The sign-off feature adds
// three cli fields — exercise both copies so the tracked duplication can
// only pass when the two stay identical.
const PARSERS: { name: string; parse: typeof parseCliConfigServer }[] = [
  { name: "server.ts", parse: parseCliConfigServer },
  { name: "http-bridge.ts", parse: parseCliConfigBridge },
];

describe("parseCliConfig sign-off fields", () => {
  for (const { name, parse } of PARSERS) {
    it(`${name}: parses mcp_servers / required_signoffs / max_signoff_attempts`, () => {
      const out = parse({
        mcp_servers: [{ name: "v", url: "https://v/mcp" }],
        required_signoffs: [{ public_key: "PEM", path: "endpoints" }],
        max_signoff_attempts: 2,
      });
      expect(out?.mcpServers).toEqual([{ name: "v", url: "https://v/mcp" }]);
      expect(out?.requiredSignoffs).toEqual([
        { publicKey: "PEM", path: "endpoints" },
      ]);
      expect(out?.maxSignoffAttempts).toBe(2);
    });
  }
});

// A present-but-malformed sign-off / validator config must fail LOUDLY, not
// silently drop the malformed entry. Silent dropping is the exact failure the
// gate exists to prevent: a `required_signoffs: [{ path: "x" }]` (missing
// public_key) would parse to nothing, the gate would never fire, and unsigned
// output would resolve to terminal success. Both parser copies must throw
// CliConfigError (error_class agent/attribute_invalid) identically.
describe("parseCliConfig fails loudly on malformed gate config", () => {
  for (const { name, parse } of PARSERS) {
    describe(name, () => {
      it("throws when a required_signoffs entry omits public_key", () => {
        expect(() =>
          parse({ required_signoffs: [{ path: "endpoints" }] }),
        ).toThrow(CliConfigError);
      });

      it("throws when a required_signoffs entry has an empty public_key", () => {
        expect(() =>
          parse({ required_signoffs: [{ public_key: "", path: "x" }] }),
        ).toThrow(CliConfigError);
      });

      it("throws when a required_signoffs entry has a non-string public_key", () => {
        expect(() =>
          parse({ required_signoffs: [{ public_key: 42 }] }),
        ).toThrow(CliConfigError);
      });

      it("throws on a mixed list (one good, one malformed) rather than degrading", () => {
        expect(() =>
          parse({
            required_signoffs: [{ public_key: "PEM", path: "a" }, { path: "b" }],
          }),
        ).toThrow(CliConfigError);
      });

      it("throws when required_signoffs is present but not an array", () => {
        expect(() =>
          parse({ required_signoffs: { public_key: "PEM" } }),
        ).toThrow(CliConfigError);
      });

      it("throws when an mcp_servers entry omits name or url", () => {
        expect(() =>
          parse({ mcp_servers: [{ url: "https://v/mcp" }] }),
        ).toThrow(CliConfigError);
        expect(() =>
          parse({ mcp_servers: [{ name: "v" }] }),
        ).toThrow(CliConfigError);
      });

      it("the thrown CliConfigError carries error_class agent/attribute_invalid", () => {
        try {
          parse({ required_signoffs: [{ path: "endpoints" }] });
          throw new Error("expected parse to throw");
        } catch (e) {
          expect(e).toBeInstanceOf(CliConfigError);
          expect((e as CliConfigError).errorClass).toBe("agent/attribute_invalid");
        }
      });

      it("does NOT throw when the gate fields are absent (no gate configured)", () => {
        // Field-absent is distinct from field-present-but-malformed: an
        // absent required_signoffs / mcp_servers means no gate, which is a
        // legitimate ungated run, not a misconfiguration.
        expect(parse({ permission_mode: "bypassPermissions" })).toEqual({
          permissionMode: "bypassPermissions",
        });
        expect(parse({})).toBeUndefined();
      });
    });
  }
});

describe("advertised sign-off surface", () => {
  it("declares the agent/signoff_unobtained error class", () => {
    expect(declaredErrorClasses).toContain("agent/signoff_unobtained");
  });

  it("exposes mcp_servers and required_signoffs in the cli schema", () => {
    const cliProps = expectedAttributesSchema.properties.cli.properties as Record<
      string,
      unknown
    >;
    expect(cliProps).toHaveProperty("mcp_servers");
    expect(cliProps).toHaveProperty("required_signoffs");
  });
});
