---
issue: stories-non-canonical-heading-shape
kind: human
category: docs-drift
artifacts:
  - design/stories/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# 122 of 123 story files ignore the official template — which side is right?

Rimsky's story documents (one durable user promise per file) are authored against a template from the planning toolchain, which prescribes a single `## Story` section reading "As \<role\>, I want \<capability\>, so that \<benefit\>." The project's actual practice is different: 122 of 123 story files split that into three headings — `## Role` / `## Capability` / `## Business value` — and say "I can" instead of "I want." Exactly one file follows the official shape. Nothing else differs: the trailing Acceptance/Falsifier/Proof sections are identical either way, so converting in either direction is a mechanical, lossless rewrite. No decision anywhere records the divergence as deliberate — but 122-to-1 is hard to read as accident.

The choice matters because a compliance review flags the split shape on every pass — this issue exists because one such review flagged one file, and fixing that one file alone would just create inconsistency. The template lives in the planning toolchain, which the project's owner also controls, so "change the rule" is genuinely available, not just theoretically.

## Options

- **Sweep the 122 files to the official shape** — one large, uniform, low-risk edit; ends the flagging; discards a format the project evidently prefers.
- **Bless the house style**: change the toolchain's template to the three-heading, "I can" shape, and convert the single official-shaped outlier to match. One upstream edit versus 122 local ones.
- **Split the question** — headings and verb ("I can" vs "I want") are separable; a ruling could change one and keep the other.

The ruling decides which shape is canonical, and whichever way, whether the conversion is one work item or its own pass.

## Ruling

> Recommended ruling (/recommend-rulings): Keep
> Role/Capability/Business-value with the 'I can' verb as the
> deliberate house style: change the plugin's STORY-TEMPLATE upstream
> to this shape, and convert the one canonical-shaped outlier
> (stories/sensor-object-store.md) to house style.
>
> Rationale: 122-to-1 is the project's expressed taste, and the owner
> controls the upstream template — aligning the rule to the practice
> is one edit where the sweep is 122, with nothing semantic gained.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
