---
issue: story-bundled-park-resume-recipe-mechanism-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:bundled-park-resume-recipe
status: verified
opened: 2026-08-01T22:31:30Z
---

# The park-resume recipe's production-path commitment needs a home before the story can reduce

The bundled park-resume recipe is a shipped example that demonstrates a node parking (pausing on an upstream rate limit) and resuming, so a user can watch the behavior before wiring a real rate-limited upstream. Its story carries a clause past the canonical sentence: the recipe induces its park through the production parking path, not a synthetic conformance probe. That clause is the recipe's whole credibility — a demo that parks via a test hook demonstrates nothing about production behavior — and no decision or concept states it anywhere else.

The format rules force the story down to its sentence, which would orphan the clause. And the clause qualifies as a decision on its own terms: a synthetic probe is a real alternative (cheaper, more deterministic) that was identifiably rejected in favor of fidelity. The authoring rules therefore determine the resolution — home the commitment as a recorded decision, then reduce the story — but creating a decision artifact changes the artifact set, which only a sprint may do.

## Options

- Record the choice as a decision (bundled recipes exercise production paths, not probes) and reduce the story — the rule-forced shape.
- Rule the clause below corpus altitude and reduce anyway — discards a commitment with a genuine tradeoff, the kind decisions exist to hold.

The ruling confirms the rule-forced homing. Shares the stray-commitment pattern with `issue:story-claude-agent-restructure`, `issue:stories-bundled-sensor-collapse`, and `issue:stories-claim-producer-backend-collapse` — worth ruling as one policy.

## Ruling

> Generated ruling (/verify-issues): record a decision that bundled recipes induce their demonstrated behavior through production paths, never synthetic probes — with the probe alternative named and rejected — then reduce the recipe's story to its canonical sentence. The authoring rules force this: the clause is a real choice with a real alternative, stories may not carry prescriptive prose, and dropping the clause would silently discard a commitment.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
