---
issue: story-sensor-object-store-generic-vs-bundled-tech
kind: audit
category: unclear
artifacts:
  - story:sensor-object-store
status: repaired
opened: 2026-07-25T03:18:31Z
---

# Does `story:sensor-object-store`'s catalog summary still advertise a technology commitment ("object-store-driven") the story body deliberately refuses to make?

Yes, and it has been repaired. The story body is already in the current single-statement form and deliberately names no technology ("As an operator, I want my workflow to react when new content is deposited into a location I designate, so that upstream producers can hand work to the graph by simply dropping it there — without anyone writing custom integration code."). Only the `.ok-planner/design/stories.md` TOC one-line summary was stale, still reading "Operator wires object-store-driven message." This is a stale-TOC-line repair per `{{MECHANICAL-VS-JUDGMENT-RULE}}`: it changes only how the existing (deliberately tech-agnostic) commitment is summarized, not what the story commits to. Changed the TOC line to "Operator wires deposited-content-driven message." The slug stays as-is (a navigation name, not a body claim), and the body's technology-is-a-decision framing is unchanged. Prose-only fix, no build/test surface.
