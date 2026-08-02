---
audit: network-binding
artifact: decision:network-binding
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:12Z
---

# Control-API loopback default, overridable bind, ephemeral one-shot port, and fail-fast supervisor callback advertise host

Supported. `lib/control/config/controlapi.go::StartControlAPI` defaults `cfg.Host` to `127.0.0.1` when unset and takes the bind address from configuration otherwise, matching the loopback-default-but-overridable claim; the shipped all-in-one image is one instance of the wide-bind split/production posture the Choice describes. The one-shot self-host launcher (`cmd/rimsky/cli/compose/run.go`) picks a kernel-assigned free port via `hostagent.FreeLocalPort` and retries on bind conflict up to 3 attempts (`startRoleStackWithBindRetry`, gated on `isBindInUseErr`), rather than using a fixed port. The supervisor's callback listener (`lib/control/launch/supervisor.go::resolveSupervisorConfig`) defaults its bind host to `0.0.0.0` when unset, and `lib/runtime/supervisor.go::effectiveCallbackHostPort` returns a hard error — aborting `StartSupervisor` before it registers — when the bind host is a wildcard and no advertise host is configured, exercised by `lib/runtime/callback_advertise_test.go::TestEffectiveCallbackHostPort_FailsFastOnWildcardBindWithoutAdvertise` alongside 3 other tests covering the resolution, persistence, and the mTLS-conditional `https://` scheme of the advertised URL.
