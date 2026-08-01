---
issue: concept-conformance-enumerates-scenario-names
kind: audit
category: other
artifacts:
  - concept:conformance
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: does the conformance concept's per-protocol prose — naming and narrating nearly every individual test scenario — violate the house rule against concepts enumerating their own instances, and where do its two genuinely load-bearing policies (the stub-mode fail-closed gate, the raw-wire fallback probe) belong given they were partly duplicated into Invariants already?

Rule that determined the fix: `CONCEPT-DEFINITION`/`SELF-CONTAINMENT-RULE` bars concepts from enumerating "wire-format identifiers" and reading as "a list of things that currently exist"; `DECISION-DEFINITION` separately bars decisions from being specs ("does not enumerate implementation steps ... call sequences"), which forecloses the issue's "relocate to a decision" option — narrated test mechanics have no licensed corpus home, only a shape-level description does. Genuine commitments belong in `## Invariants` exactly once, per the corpus's own precedent (`concept:signal`'s "membership of the set is owned by the code, not enumerated here," naming individual members only where they carry a distinct commitment).

What changed: `.ok-planner/design/concepts/conformance.md` — each of the seven per-protocol bullets was cut from full narrated-scenario prose to a shape-level description carrying its scenario count and categories only (e.g. "Nine registered scenarios spanning the happy path, async handoff and restart survival, cancellation, attribute and terminal-tag round-trips, scratch park-and-resume, park emission, and malformed input"). The raw-wire fallback probe, previously stated only inside the claim-producer bullet's prose (not in Invariants, so not actually a duplication issue there — it was doing the opposite: appearing once at the wrong altitude), was added as its own `## Invariants` bullet, and the claim-producer bullet now points there instead of restating it. The require-stub-mode escalation policy was already fully stated in Invariants; its "What it is" mention was trimmed to a cross-reference. No new decision files.

How verified: re-read the file for internal consistency — Boundaries' claim ("owns the library and handler structure, not a scenario list") now matches the body; every policy named in Invariants appears exactly once in the file. Markdown-only change, no build/test impact.
