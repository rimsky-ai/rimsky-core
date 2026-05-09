// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-mcp-control-api is the bundled MCP shim that wraps the
// rimsky control-API. Reads CONTROL_API_URL + CONTROL_API_TOKEN env
// vars and listens on PORT (default 8081).
//
// Per plan K1 + K4.

package controlapimcp

// (Empty intentionally — main entrypoint lives in cmd/rimsky-mcp-control-api/main.go
// inside this module, kept separate from the package so the package can
// be imported by tests and out-of-process composers.)
