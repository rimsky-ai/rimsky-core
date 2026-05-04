// Package migrations embeds the Postgres migration tree.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
