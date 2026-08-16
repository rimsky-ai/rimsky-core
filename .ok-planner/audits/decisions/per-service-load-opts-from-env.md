---
audit: per-service-load-opts-from-env
artifact: decision:per-service-load-opts-from-env
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# One shared env-options loader per bundled service, called by both surfaces

Supported. Sweeping the services module for env-options loaders gives exactly six, one per dual-mode bundled service — the four executors and the two claim producers — and no service has a second. Each is called from precisely two places: that service's standalone entrypoint, and the single bundled registration entrypoint, so neither surface parses the same variables twice and both construct the handler from identical options. The unconfigured-versus-misconfigured split is real and asymmetric exactly as claimed. Both claim producers carry a configured flag on their options that is false when their config path variable is unset, and the CLI-spawning executor reports credentials unconfigured when neither auth variable nor stub mode is set. On the bundled path each of those three conditions produces a log line and a skip that leaves the rest of the registration running; on the standalone path the same condition is fatal — the two producer entrypoints exit naming the missing variable, and the executor's serve function returns the credentials-missing error, which its entrypoint exits on. Present-but-invalid configuration returns an error from the loader itself, which the standalone entrypoints exit on and the bundled entrypoint turns into a boot abort naming the service. Tests in the bundled package cover the zero-config skip for both producers and the executor, the invalid-config abort naming the producer, and the registration failure naming the executor.
