---
issue: cli-api-key-exception-list-omits-ephemeral-run-self-host
kind: audit
category: conflicting
artifacts:
  - concept:rimsky
  - decision:rimsky-run-self-hosts-templates
status: retired
opened: 2026-08-19T21:29:32Z
---

# Where the corpus records that the ephemeral-run verb's self-host branch presents no credential

The CLI's api-key rule says every verb that dials the control API defines the key flag and sends the resolved key. The rule's concept closes its exception list with "These verbs stand outside the rule, and no others do" and names six groups. A mechanical test walks every verb and enforces the list. The ephemeral-run verb's self-host branch is exempted in the test and appears in none of the six groups, so the test enforces a list the corpus does not state.

The verb runs in two modes. Its remote branch delegates to the remote-run verb, which the test checks on its own. Its self-host branch boots an in-process stack and reaches it over loopback; the stack it just booted holds no operator credential, and sending one would fail the run for an operator whose environment names a key for a remote deployment. The behavior is right; only the record is missing. The concept (`concept:rimsky`) and the decision behind the verb (`decision:rimsky-run-self-hosts-templates`) both say the ephemeral-run verb reuses the compose one-shot's self-host machinery, and the compose one-shot is already a named exception for exactly this loopback reason.

State of play: the exemption sits in the test with its justification stated at the site; the concept's list is stale by one entry; nothing else diverges.

## Options

- Name the ephemeral-run verb's self-host branch as a seventh exception in the concept's list. Cost: the list grows one verb-shaped entry for what is really a mode.
- Restate the exception as the self-host one-shot machinery both modes share, so the clause covers the mode rather than each verb. Cost: the list mixes one mode-shaped entry among verb-shaped ones.
- Keep the list closed on verbs and restructure the verb so its self-host branch routes through the compose one-shot's entry point, removing the exemption. Cost: a code restructuring to repair prose, and the verb's flag surface must survive the reroute unchanged.

The ruling decides where the corpus records a loopback exemption the code already makes.

## Ruling

> Retire: the concept-catalog repair dissolves this issue. Its question is where a closed verb-exception list records a seventh entry. The repair (issue `concept-catalog-carries-non-definitional-content`) removes that list from `concept:rimsky` as the enumeration the rule forbids. The mechanical test that walks every verb owns the exception set's membership, and the exemption already sits there with its justification stated at the site. Ruled live by the owner, 2026-08-20.
