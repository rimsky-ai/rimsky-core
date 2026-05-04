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
