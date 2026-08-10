---
audit: validation-warnings-surfaced
artifact: story:validation-warnings-surfaced
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Static advisories carried back to the template author and promotable

Supported. One template tripping a single static advisory and nothing else was
put through both responses the story names, with and without the promotion
flag, against a zero-config all-in-one deployment. The validation response
carried the advisory and answered ok; with the flag it answered not-ok and
still named the advisory. The registration response carried the advisory and
registered the template; with the flag it refused with the advisory named, the
flag echoed, and no template row persisted. The command-line lint verb printed
the advisory and flipped its verdict under the flag, and the register verb
refused under the flag with the advisory printed. One narrowing: the register
verb's own rendering of a successful registration prints the template id alone,
so a template author who registers from the command line and is not refused
sees no advisory, while the response that verb read carries it.

## Compliance

Two defects. The body names the delivery surface — the registration and
validation responses — which is the decision's territory, not the story's. It
also frames a change rather than a durable expectation: "the existing flag" and
"advice the validator already computes" describe a gap being closed. Compliant
text: "As a template author, I can see the advisories the validator computes
about my template, and choose to have them refuse the template outright, so
that I decide whether advice is worth acting on before I run anything."
