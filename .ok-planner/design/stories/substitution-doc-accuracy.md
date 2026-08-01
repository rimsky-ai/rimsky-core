---
story: substitution-doc-accuracy
status: as-is
---

# Substitution doc matches resolver

## Story

As a template author reading the substitution documentation, I can trust that the listed source kinds match exactly what the resolver actually recognizes, so that I don't silently miss a supported source.

Automated accuracy gate: the documented list of substitution source kinds matches the runtime resolver's dispatch set.

Template authors don't silently miss supported substitution sources because the doc undercounts; the gate catches drift at build time, not at template-author confusion time.
