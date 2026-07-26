---
issue: decisions-historical-language-residue
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# Sixteen decisions carry changelog or retired-implementation narration

The current-state-only rule bars historical narration — git records what changed. Sixteen decisions still narrate: "the previous model," "was considered and retired unused," "mirroring the retired TypeScript implementation." Two deserve care: `decision:typescript-claude-agent-retirement` is entirely a changelog of a deletion event with no forward-looking choice left (a candidate for retirement rather than rewrite), and `decision:signoff-crypto-ed25519` anchors a genuinely load-bearing wire-compatibility guarantee on "the retired TypeScript implementation's test vectors" — the guarantee must survive the rewrite even as the historical framing goes.

The full list is in this issue's git history (the as-filed body). The intent-directory question filed alongside this one is the same failure mode at directory scale.

## Options

- A remediation sprint restates each in current-state terms, retiring `decision:typescript-claude-agent-retirement` outright and rephrasing the signoff compatibility anchor as a present-tense wire-contract statement. Cost: one focused sprint pass.
- Leave the narration — a standing violation of a rule the corpus enforces everywhere else.

## Ruling

> Generated ruling (/verify-issues): a sprint rewrites the sixteen decisions in
> current-state terms — retiring the purely-historical TypeScript-retirement decision,
> and preserving the ed25519 wire-compatibility guarantee in present-tense form.
> The current-state-only rule forces this; only the editing is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
