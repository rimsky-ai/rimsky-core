# Error catalog

One file per consumer-observable error. Each file states what the error means, when it happens, and what to do.

*Internal-correctness errors* (state-machine rejections, sweep-internal errors, advisory-lock failures) are not listed here — they are not consumer-observable.

## Index

- [`orphaned_claim_lost_race.md`](orphaned_claim_lost_race.md) — supervisor lost ownership of a claim mid-execution.
- [`capability_envelope_mismatch.md`](capability_envelope_mismatch.md) — operator-declared envelope is not a subset of producer-advertised envelope.
- [`tag_shape_rejected.md`](tag_shape_rejected.md) — a tag identifier in the `sha256-<64-hex>` shape was rejected.
- [`compose_prefix_violation.md`](compose_prefix_violation.md) — non-compose caller used the reserved `compose:<project>:` prefix.
- [`schedule_cron_parse_failure.md`](schedule_cron_parse_failure.md) — schedule cron expression failed to parse.
- [`template_not_deployed.md`](template_not_deployed.md) — instance creation against a registered-but-not-deployed template.
- [`stub_mode_probe_failed.md`](stub_mode_probe_failed.md) — conformance run rejected a non-stubbed executor.
- [`async_callback_wrong_key.md`](async_callback_wrong_key.md) — executor posted async callback with `kind:` instead of `type:`.
- [`attribute_validation_failed_at_dispatch.md`](attribute_validation_failed_at_dispatch.md) — substituted attributes failed schema validation at dispatch.
- [`attribute_validation_failed_at_commit.md`](attribute_validation_failed_at_commit.md) — executor writeback failed schema validation at commit.
- [`heartbeat_lost.md`](heartbeat_lost.md) — supervisor or executor heartbeat went silent past the timeout.
- [`unresolved_executor.md`](unresolved_executor.md) — node references an executor that is not configured in `rimsky.yml`.
- [`schedule_dispatch_failed.md`](schedule_dispatch_failed.md) — scheduled fire could not dispatch the target node.
