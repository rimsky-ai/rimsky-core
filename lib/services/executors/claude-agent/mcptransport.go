// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import "fmt"

const (
	mcpTransportHTTP         = "http"
	mcpTransportStdio        = "stdio"
	mcpTransportModule       = "module"
	mcpTransportHTTPLoopback = "http-loopback"
)

func unknownMcpTransportError(entryDesc string, transport string) *CliConfigError {
	return &CliConfigError{Message: fmt.Sprintf(
		"%s has unknown transport %q (expected %s | %s | %s | %s)",
		entryDesc, transport, mcpTransportHTTP, mcpTransportStdio, mcpTransportModule, mcpTransportHTTPLoopback)}
}
