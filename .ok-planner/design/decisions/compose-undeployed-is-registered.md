---
decision: compose-undeployed-is-registered
---

# `undeployed` in a compose manifest means `registered`

## Choice

A compose manifest's `templates[].state` accepts `registered`, `deployed`, and `undeployed`. `undeployed` is a synonym for `registered`: the template stays registered under its tag and holds no deployment. The state is declarative in both directions: a template the deployment holds at `deployed` and the manifest declares `registered` or `undeployed` plans an undeploy step on the next apply, and a template the manifest declares `deployed` plans a deploy.

## Rationale

An operator who wants a template out of service writes the word that says so. `undeployed` names the outcome the operator wants; `registered` names the state the deployment ends in. Both words describe one state, so accepting both costs one map entry and spares the operator a lookup. A state key that moved a template forward and never back would make the manifest a script, not a declaration.

## Alternatives

- A third state that also drops the tag — rejected: dropping a tag is removal, and a manifest already expresses removal by omitting the entry.
- Reject `undeployed` as unknown — rejected: the word is the one an operator reaches for, and the error teaches nothing the synonym does not.
- One-way state, deploying only — rejected: a manifest that cannot undeploy is not declarative.
