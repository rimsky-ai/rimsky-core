---
audit: http-router-chi
artifact: decision:http-router-chi
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:46Z
---

# go-chi/chi is the HTTP router, pinned to its stable v5 line

Supported. The root `go.mod` pins `github.com/go-chi/chi/v5 v5.3.0` (with a matching `go.sum` entry) as the sole HTTP router dependency; `lib/control/controlapi/app.go` and the other route-registering files build on `chi.NewRouter`/`chi.Router` throughout, composing standard `http.Handler` middleware rather than a framework-specific abstraction. A repo-wide search found no `gorilla/mux`, `gin-gonic`, or `labstack/echo` import anywhere in the tree, and the bundled services that run their own small routers (e.g. `lib/services/sensors/sensor-webhook`) use the same chi router rather than a second idiom.
