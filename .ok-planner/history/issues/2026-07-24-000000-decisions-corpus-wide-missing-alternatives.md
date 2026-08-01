---
issue: decisions-corpus-wide-missing-alternatives
kind: audit
category: proof
artifacts:
  - design/decisions/*
status: promoted
sprint: 2026-08-01-guidance-realignment-drain.md
opened: 2026-07-24T00:00:00Z
---

# 131 "decisions" never say what they decided against

A decision document in rimsky's design corpus records a real technical choice, and its mandatory Alternatives section — the option genuinely considered and rejected — is what keeps the catalog honest: a choice with no real alternative is a default, and the authoring rules say defaults get retired from the catalog, not documented. Re-counting today finds 131 of 239 live decisions missing the section entirely. Some are library picks where the rejected option is obvious and just needs writing; others are genuine defaults that should be deleted. The rules fully determine the algorithm — per file, author real Alternatives where a genuine option existed, retire as a default where none did — but classifying 131 files and executing the retirements is a bundle of intent-level corpus mutations only a sprint may make.

The disposition this file previously leaned on is void: it planned to run jointly with the missing-Proof sweep, and that sibling closed `answered` when the Proof requirement was retired outright — decisions now carry Choice/Rationale/Alternatives and nothing else, with verification owned by the periodic implementation audit. What replaces that pairing is a different joint read: two sibling issues (`issue:decisions-spec-altitude-mechanism-detail`, `issue:decisions-historical-language-residue`) require re-reading largely the same decision population for altitude and tense problems, and `decision:typescript-claude-agent-retirement` is a retirement candidate under both this issue's default test and the residue issue's. One read per file covering all three gaps is the whole efficiency argument. No automated check catches a missing Alternatives section, so whatever backlog remains can regrow silently.

## Options

- **One joint per-file pass with the two sibling decision sweeps** — author real Alternatives where an option existed, retire defaults, fix altitude and tense in the same read. A file missing Alternatives *and* sitting at spec altitude is the strongest retirement candidate.
- **A standalone Alternatives-only pass** — reads 131 files that two sibling issues will each read again.
- **Bulk-author Alternatives for all 131** — fast, and manufactures plausible filler that defeats the section's purpose.
- **Loosen the rule** — makes Alternatives optional; changes what compliant means instead of reaching it.

The ruling decides the pass's shape; the direction is forced.

## Ruling

> Generated ruling (/verify-issues): run one joint per-file
> remediation pass over the decision catalog with
> issue:decisions-spec-altitude-mechanism-detail and
> issue:decisions-historical-language-residue — per file, author
> real Alternatives where a genuine rejected option existed and
> retire the file as a default where none did. The
> decision-definition's default test forces the two-case algorithm;
> only the editing is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
