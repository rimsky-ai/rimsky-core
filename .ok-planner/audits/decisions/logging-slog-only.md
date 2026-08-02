---
audit: logging-slog-only
artifact: decision:logging-slog-only
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:17Z
---

# Logging is stdlib log/slog exclusively

Supported. 80 non-test `.go` files repo-wide import `log/slog`; zero files import `go.uber.org/zap` or `github.com/rs/zerolog`, and neither appears in any of the five modules' `go.sum` files, so the rejected alternatives are not even present as transitive dependencies.
