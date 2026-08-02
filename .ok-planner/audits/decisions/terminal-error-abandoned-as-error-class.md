---
audit: terminal-error-abandoned-as-error-class
artifact: decision:terminal-error-abandoned-as-error-class
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:36Z
---

# `terminal/error/abandoned` is an error-class signal, not a new root signal

Supported. The held-claim auto-terminal abandon path builds its cascade signal through the same generic terminal-error-signal constructor every other error class uses, producing the exact type path `terminal/error/abandoned` rather than a distinct root; no separate "abandoned" signal-type constant or root exists anywhere the terminal-error builder is not called. The general signal-subscription matcher (used uniformly for every subscription, not special-cased for abandon) supports both exact-path and trailing-wildcard prefix matching, so `terminal/error/abandoned` is reachable both by an exact subscription and by any `terminal/error/*`-or-broader wildcard subscription without additional code. An end-to-end scenario test drives a held-claim rollback to termination and asserts the acquirer node's only audited terminal event is `terminal/error/abandoned`, that a sibling subscribed via the broad wildcard `terminal/*` sees it exactly once (not double-fired), and that a separate observer subscribed to the exact path `terminal/error/abandoned` dispatches only after that event, and not on the earlier held-transition moment.
