---
story: held-commit-cascades-success
---

# Downstream sees held work's success only on commit

## Story

As a template author whose downstream node depends on data an upstream's held work produces, I can rely on the upstream's success signal reaching my subscriber only when the held work has committed — never at the provisional held moment — so that downstream never acts on results a later abandon would roll back.
