---
issue: decisions-historical-language-residue
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# Sixteen decisions carry changelog or retired-implementation narration

The design corpus's current-state-only rule bars historical narration — git records what changed; the artifacts describe the project as it stands. Sixteen decisions still narrate: "the previous model," "was considered and retired unused," "mirroring the retired TypeScript implementation." Two deserve care. `decision:typescript-claude-agent-retirement` is entirely a changelog of a deletion event with no forward-looking choice left — re-verified today, it is a clean retirement candidate under the decision-definition's own default test (nothing is chosen; the event is history). And `decision:signoff-crypto-ed25519` anchors a genuinely load-bearing wire-compatibility guarantee on "the retired TypeScript implementation's test vectors" — the guarantee must survive the rewrite as a present-tense wire-contract statement even as the historical framing goes.

The full as-filed list survives in this file's git history. The work is a per-file rewrite of the same decision population two sibling issues already require reading (`issue:decisions-corpus-wide-missing-alternatives`, `issue:decisions-spec-altitude-mechanism-detail`) — `decision:typescript-claude-agent-retirement` is a retirement candidate under both this issue's test and the Alternatives issue's default test, which is the concrete case for one joint pass. The direction is rules-forced; the retirement and the rewrites are intent-level sprint work.

## Options

- **Fold into the joint decisions pass** — each of the sixteen restated in current-state terms during the same read the sibling sweeps require; the TypeScript-retirement decision retires outright; the ed25519 anchor rephrases present-tense.
- **Leave the narration** — a standing violation of a rule the corpus enforces everywhere else.

The ruling confirms the forced direction and the joint scheduling.

## Ruling

> Generated ruling (/verify-issues): rewrite the sixteen decisions in
> current-state terms inside the joint decisions pass with
> issue:decisions-corpus-wide-missing-alternatives and
> issue:decisions-spec-altitude-mechanism-detail — retiring the
> purely-historical decision:typescript-claude-agent-retirement, and
> preserving the ed25519 wire-compatibility guarantee as a
> present-tense contract statement. The current-state-only rule
> forces the direction; only the editing is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
