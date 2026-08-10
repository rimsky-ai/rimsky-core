---
experiment: publisher-protocol
commit: PENDING
---

# A third-party publisher feeds a workflow and keeps its subscription

## What it ran against

`peer/` is a complete third-party publisher: its own Go module whose only rimsky
requirement is the protocols module, built the way the permissive-peer-build
experiment's executor is. It serves the four publisher verbs, keeps its
subscriptions in its own process, and posts messages into the workflows it is
subscribed for through the control API. `run.sh` cross-builds it, runs it beside
a `rimsky-all-in-one` stack from the tree's own image tag whose only knowledge
of it is one `publishers:` entry, and drives everything else through the control
API and the publisher's own state endpoint.

## What was observed

The publisher's advertisement gates the templates that may name it: a template
declaring a kind the publisher never advertised was rejected as
`publisher_unadvertised_kind`. A template declaring the advertised kind
registered and deployed.

Creating an instance made rimsky call Subscribe exactly once, and the
subscription carried the instance it was for, the template's kind, its message
type and its resolved config. The message the publisher then posted woke the
subscribing node; rimsky recorded the message as coming from the publisher it
knows, with sender kind `publisher` rather than an operator, and the publisher's
own payload on the record.

Restarting the rimsky container did not re-issue the subscription. The restarted
stack asked the publisher what it already held, and after that reconciliation
Subscribe was still at one call, the publisher still held the same subscription
id, and Unsubscribe had not been called. The publisher's next message landed the
same way, running the subscribing node a second time. Terminating the instance
released the subscription: Unsubscribe was called once and the publisher was
left holding none.

Nineteen checks, none failing.
