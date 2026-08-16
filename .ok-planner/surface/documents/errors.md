---
document: errors
target: docs/errors/
---
# Error catalog

## Purpose
An operator or template author holding an error class from an event, a terminal signal, or an API rejection opens its page to learn what produced it, whether an error_types entry can act on it, and what to change. They close it with the class routed or the misconfiguration fixed. One page per error class (or per mechanism family where one code path emits several related classes), plus an index keyed on the literal class strings.

## Covers
- every consumer-observable error class the runtime synthesizes
- every error class the bundled services declare in their capabilities handshake
- the public template keys that route errors
- the public HTTP routes whose synchronous rejections a caller must handle
