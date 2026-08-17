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

The tree ships no copy-runnable park-then-resume recipe. A story promises an operator can copy and run a bundled recipe that parks a node on a rate-limited upstream and resumes it, on the bundled stack, without writing anything. A decision requires that recipe to induce a real park through production paths, never a synthetic probe. The behaviour works: the audit drove park, self-resume and success on the bundled stack. The last sprint deleted the examples module that carried the recipe, along with the whole examples tree. The five remaining demo scripts under test fixtures touch no parking, and the tree holds no docs, examples or recipes directory. Both artifacts point at a recipe that no longer exists. The ruling decides whether this repo still ships recipes.

## Options

- Build the recipe as a work item and keep both artifacts. The recipe is a template, a rate-limited endpoint, and a script; cost: a maintained example in a tree that just removed its examples module.
- Retire the story and the decision; cost: the evaluator loses the "try it in one command" path.
- Keep the decision as a standing rule for any future recipe, drop its park-then-resume example, and retire the story until a recipe exists; cost: a rule with no current subject.

The ruling decides whether bundled recipes are part of the product.

## Ruling

> Recommended ruling (/verify-issues): Retire the story and rewrite the decision as a standing rule for any recipe the project ships later. The release documentation ceremony's cookbook and examples types, declared this run, are where copy-runnable material now lands, generated from the tree at each release rather than maintained by hand.
>
> Rationale: the project removed the examples module on purpose. The documentation run now produces the artifact this story wanted, from measured experiments. A hand-kept recipe would duplicate it. Flip case: if the owner wants a recipe that runs without the docs, an in-repo demo script beside the five that exist, take the first option and name the fixture directory as its home.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
