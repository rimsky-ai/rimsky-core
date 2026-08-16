---
issue: no-shipped-park-resume-recipe
kind: audit
category: conflicting
artifacts:
  - story:bundled-park-resume-recipe
  - decision:bundled-recipes-production-paths
status: verified
opened: 2026-08-16T08:55:53Z
---

# The corpus promises a copy-runnable park-then-resume recipe that the tree does not ship

A story promises an operator can copy and run a bundled recipe that parks a node (rate-limited upstream) and resumes it, on the bundled stack, without writing anything; a decision requires that recipe to induce a real park through production paths, never a synthetic probe. The behaviour works — the audit drove park, self-resume and success on the bundled stack — but nothing copy-runnable ships: the examples module that carried the recipe was deleted with the whole examples tree in the last sprint, the five remaining demo scripts under test fixtures touch no parking, and there is no docs, examples or recipes directory. Both artifacts point at an artifact that no longer exists. The ruling decides whether recipes are still a category this repo ships.

## Options

- Build the recipe as a work item (a template, a rate-limited endpoint, and a script), keeping both artifacts; cost: a maintained example in a tree that just removed its examples module.
- Retire the story and the decision; cost: the "try it in one command" evaluator path is gone.
- Keep the decision as a standing rule for any future recipe, drop its park-then-resume example, and retire the story until a recipe exists; cost: a rule with no current subject.

The ruling decides whether bundled recipes are part of the product.

## Ruling

> Recommended ruling (/verify-issues): Retire the story and rewrite the decision as a standing rule for any recipe the project ships later; the release documentation ceremony's cookbook and examples types (declared this run) are where copy-runnable material now lands, generated from the tree at each release rather than maintained by hand.
>
> Rationale: the examples module was removed on purpose, and the documentation run now produces the artifact this story wanted, from measured experiments; a hand-kept recipe would duplicate it. Flip case: if the owner wants a recipe that runs without the docs (an in-repo demo script beside the five that exist), take the first option and name the fixture directory as its home.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
