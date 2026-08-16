---
audit: rimsky-compose-run-scope
artifact: decision:rimsky-compose-run-scope
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The compose one-shot's manifest-only input surface and the untouched lifecycle verbs

Supported. The compose one-shot's flag set carries a run name, an artifact-root override, a timeout, the three progress flags, and a repeatable late-bound-service flag — no template flag anywhere — and it requires exactly one positional argument, which it treats as a manifest path and hands to the manifest loader; there is no content inspection, no format sniffing, and no fallback branch that would let a template file through, so the rejected autodetection does not exist. The single-template case is covered by the separate ephemeral-run verb, which self-hosts on its own machinery. The compose dispatcher still routes the four lifecycle verbs alongside the one-shot, each to its own handler.
