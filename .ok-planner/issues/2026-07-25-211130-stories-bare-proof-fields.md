---
issue: stories-bare-proof-fields
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# Twenty-nine stories have a category label where their Proof statement should be

Every story's Proof field is meant to state what a third party must observe to confirm the story holds — it is the canonical statement the falsifier-exhibition discipline runs against. Twenty-nine story files instead carry only a category label ("Executable proof." / "Example." / "Demo."). Spot-checks confirm the pattern and also that each file's own Acceptance and Falsifier text contains the substance a real Proof sentence would state — the fix is transcription with judgment, not new design.

A label cannot drive proof exhibition: nothing in "Executable proof." tells a prover what red would look like. The affected files are listed in this issue's git history (the as-filed body); they span the auth, claim-producer, compose, host-agent, and observability story families.

## Options

- A remediation sprint writes a substantive Proof sentence for each of the 29, drawn from each story's own Acceptance/Falsifier. Cost: one focused sprint pass.
- Amend the story form to allow category labels — would gut the proof-protection mechanism corpus-wide.

## Ruling

> Generated ruling (/verify-issues): a sprint rewrites all 29 bare Proof fields into
> substantive third-party-observable statements derived from each story's Acceptance
> and Falsifier. The story form's mandatory-Proof rule leaves no other compliant end state.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
