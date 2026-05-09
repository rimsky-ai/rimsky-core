// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// config.go declares the env-var schema for the bundled MCP shim.
//
// Per plan K4. The shim's binary entry point in
// cmd/rimsky-mcp-control-api/main.go reads from these names; this file
// keeps the canonical names colocated with the package so embedders
// who consume the shim as a Go module can do likewise.

package controlapimcp

// EnvVar names recognised by cmd/rimsky-mcp-control-api/main.go.
const (
	// EnvControlAPIURL is the absolute base URL of the rimsky control-API.
	// Default: "http://127.0.0.1:8080".
	EnvControlAPIURL = "CONTROL_API_URL"
	// EnvControlAPIToken, when set, is forwarded as
	// `Authorization: Bearer <token>` on every wrapped HTTP call. Empty
	// means no Authorization header is sent.
	EnvControlAPIToken = "CONTROL_API_TOKEN"
	// EnvBindAddr is the bind address. Default: "0.0.0.0".
	EnvBindAddr = "BIND_ADDR"
	// EnvPort is the listen port. Default: 8081.
	EnvPort = "PORT"
)
