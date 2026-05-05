// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// embedded.go — //go:embed boundary for init scaffold assets.
//
// The CLI binary embeds the rimsky module's reference deploy files
// (`deploy/docker-compose.yml`, `deploy/store-filesystem.yml`,
// `deploy/supervisor-config.yml`) plus a minimal example graph and
// the `rimsky-compose.yml` template scaffold. Refresh the embedded
// copies via `make cli-sync-embedded`.
package embedded

import "embed"

//go:embed deploy graphs rimsky-compose.yml.tmpl
var FS embed.FS
