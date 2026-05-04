module github.com/fallguy/rimsky/foundation

go 1.25.0

require (
	github.com/fallguy/rimsky/protocols v0.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
)

replace github.com/fallguy/rimsky/protocols => ../protocols
