---
audit: bundled-park-resume-recipe
artifact: story:bundled-park-resume-recipe
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:32:02Z
---

# Operator has a copy-runnable park-then-resume recipe on the bundled stack

Supported. `examples/park-resume-demo.sh` is a self-contained, copy-runnable shell script (checks for `docker`/`curl`/`python3`, boots the `rimsky-all-in-one` image plus a bundled example rate-limiter image on an isolated Docker network, registers a template driving the bundled `http-node` executor at the rate-limiter, and polls node state through to a resumed `terminal/success`) requiring nothing beyond the checkout and locally built images. The park is induced by the rate-limiter's first-request 429 with `Retry-After`, hitting the `http-node` executor's production status-code handling (`server.go`: `resp.StatusCode == http.StatusTooManyRequests` → `parkedOutcome`), not a synthetic probe path (that branch is gated behind a separate stub-mode flag the demo never sets). The same mechanism is additionally proven by an ordinary Go end-to-end test, `examples/park-resume/main_e2e_test.go` (`TestParkThenResumeOnBundledStackE2E`), which boots the stack via testcontainers, drives the identical template, and asserts both a `transient/park` and a subsequent `terminal/success` audit event before passing.
